package syncconfig

import (
	"context"
	"errors"
	"fmt"

	"github.com/gv/jitenv/internal/syncadapters"
	"github.com/gv/jitenv/pkg/syncadapter"
)

// PushResult / PullResult report what an orchestration step did so the
// CLI can print user-facing messages and the agent-reload hook can fire
// only when local actually changed.
type PushResult struct {
	Hash string // hash of the config that was pushed
}

type PullResult struct {
	Decision MergeDecision
	// Applied is the config plaintext written to disk on a fast-forward
	// (nil for every other decision). The caller writes it via
	// config.AtomicSave so the agent reload hook fires.
	Applied []byte
	Hash    string // remote hash on a fast-forward / local hash otherwise
}

// PushConfig seals cfgBytes under dek and pushes via adapter, honoring
// the divergence fence unless force is set. On success it records the
// new base hash on ad (the caller persists the sidecar).
//
// dek and masterKey are NOT logged; the caller owns zeroing them.
//
// Concurrency note: the pre-push fence here is best-effort — it relies
// on the adapter's Pull reflecting whatever was last successfully
// pushed (TOCTOU). Adapters that can offer stronger compare-and-set
// guarantees layer them on top INSIDE their Push using state
// remembered from the immediately-preceding Pull: the s3 adapter
// stashes the blob's ETag at Pull-time and issues PutObject with
// If-Match on Push (#278), so two concurrent pushers from different
// hosts cannot both land — the loser sees ErrPreconditionFailed
// instead of silently overwriting. The file and ssh adapters have no
// equivalent CAS primitive available without OS-level coordination
// (flock / a distributed lock service) and remain TOCTOU-vulnerable;
// that gap is documented in pkg/syncadapter/syncadapter.go's v2 Lock
// note.
func PushConfig(ctx context.Context, adapter syncadapter.Adapter, ad *Adapter, dek, cfgBytes []byte, schemaVersion int, force bool) (PushResult, error) {
	localHash := HashConfig(cfgBytes)

	// Always Pull before Push, even on --force: the soft fence is
	// skipped under --force, but CAS-capable adapters (s3) stash the
	// observed ETag during Pull so the subsequent Push can issue
	// If-Match. Without a Pull on the force path the adapter would
	// fall back to IfNoneMatch:"*" and refuse to overwrite any
	// existing object — defeating --force entirely (#278). Errors
	// from the pre-push Pull are evaluated below; on --force the
	// errors are tolerated and the Push proceeds.
	_, rmeta, rerr := adapter.Pull(ctx)
	if !force {
		switch {
		case errors.Is(rerr, syncadapters.ErrNoRemoteState):
			// first push; fine
		case errors.Is(rerr, syncadapters.ErrRemoteStateIncomplete):
			// The remote has a blob without its meta (or vice versa).
			// Treat as a hard refusal: a non-force push would silently
			// clobber the orphan and lose whatever it was carrying. The
			// user must pass --force to explicitly overwrite (#279).
			return PushResult{}, fmt.Errorf("remote state is incomplete (blob without meta or meta without blob); inspect the remote and re-publish with `jitenv sync push --force` to overwrite, or restore the missing file manually")
		case rerr != nil:
			return PushResult{}, fmt.Errorf("pre-push remote check failed: %w", rerr)
		default:
			switch {
			case ad.BaseHash == "":
				// No recorded base (fresh machine) but the remote already
				// has state. If local differs from remote we cannot prove
				// local descends from it, so a non-force push would
				// silently clobber the remote — refuse, symmetric with
				// PullConfig's no-base fence.
				if rmeta.Hash != localHash {
					return PushResult{}, fmt.Errorf("remote already has config (%s) but this machine has no sync base; run `jitenv sync pull --adopt` first, or push with --force to overwrite", short(rmeta.Hash))
				}
			case rmeta.Hash != ad.BaseHash && rmeta.Hash != localHash:
				return PushResult{}, fmt.Errorf("remote advanced since your last sync (remote %s != base %s); run `jitenv sync pull` first, or push with --force to overwrite", short(rmeta.Hash), short(ad.BaseHash))
			}
		}
	}

	blob, err := SealBlob(dek, cfgBytes, localHash)
	if err != nil {
		return PushResult{}, err
	}
	meta := syncadapter.Meta{Hash: localHash, SchemaVersion: schemaVersion}
	if err := adapter.Push(ctx, blob, meta); err != nil {
		if errors.Is(err, syncadapters.ErrPreconditionFailed) {
			// A CAS-capable adapter rejected the write because the
			// remote object changed between our pre-push Pull and the
			// Push itself — symmetric with the soft-fence's "remote
			// advanced" branch, but enforced by the storage (#278).
			//
			// Remediation differs by mode: on a non-force push the
			// user can pull-to-reconcile or escalate to --force, but
			// when --force itself hits the CAS, suggesting --force is
			// circular — the user is already doing that. The CAS and
			// the engine's soft fence are independent layers, and
			// --force only bypasses the soft fence, not the
			// storage-level CAS. Tell the user that and ask them to
			// retry instead.
			if force {
				return PushResult{}, fmt.Errorf("remote changed during force-push (concurrent writer rejected by storage-level CAS); retry the push: %w", err)
			}
			return PushResult{}, fmt.Errorf("remote changed between pull and push (concurrent writer); run `jitenv sync pull` to reconcile and retry, or push with --force to overwrite: %w", err)
		}
		return PushResult{}, err
	}
	ad.BaseHash = localHash
	return PushResult{Hash: localHash}, nil
}

// PullConfig fetches the remote blob, decides the merge outcome, and on a
// fast-forward decrypts + integrity-checks the plaintext. It NEVER writes
// to disk — the caller does that via config.AtomicSave so the existing
// reload hook is preserved and divergence/abort leaves local untouched.
//
// adopt is the "first pull on a fresh machine" escape hatch: when there
// is no recorded base snapshot but the remote has state, the
// DecideNoBase fence normally blocks (we can't prove local descends from
// remote). With adopt=true the caller has explicitly opted to take the
// remote as authoritative, so a NoBase outcome is treated as a
// fast-forward instead of an abort. It has NO effect on true divergence
// (DecideDiverged), which always aborts.
func PullConfig(ctx context.Context, adapter syncadapter.Adapter, ad *Adapter, dek, localCfgBytes []byte, adopt bool) (PullResult, error) {
	localHash := HashConfig(localCfgBytes)

	blob, rmeta, perr := adapter.Pull(ctx)
	remotePresent := true
	if errors.Is(perr, syncadapters.ErrNoRemoteState) {
		remotePresent = false
	} else if errors.Is(perr, syncadapters.ErrRemoteStateIncomplete) {
		// Remote has only one of (blob, meta). We can't decrypt or even
		// hash-check anything, so surface a clear error instead of
		// falling through to a hash-only Decide() that would treat the
		// zero-hash meta as a valid divergence input (#279).
		return PullResult{}, fmt.Errorf("remote state is incomplete (blob without meta or meta without blob); inspect the remote and either restore the missing file or re-publish from a known-good machine with `jitenv sync push --force`")
	} else if perr != nil {
		return PullResult{}, perr
	}

	decision := Decide(localHash, rmeta.Hash, ad.BaseHash, remotePresent)
	res := PullResult{Decision: decision, Hash: localHash}

	if decision == DecideNoBase && adopt {
		decision = DecideFastForward
		res.Decision = DecideFastForward
	}

	switch decision {
	case DecideNoRemote, DecidePushNeeded:
		return res, nil
	case DecideNoop:
		ad.BaseHash = localHash
		return res, nil
	case DecideDiverged, DecideNoBase:
		return res, &DivergenceError{Decision: decision, Local: localHash, Remote: rmeta.Hash, Base: ad.BaseHash}
	case DecideFastForward:
		plaintext, err := OpenBlob(dek, blob, rmeta.Hash)
		if err != nil {
			return res, err
		}
		if got := HashConfig(plaintext); got != rmeta.Hash {
			return res, fmt.Errorf("remote blob hash mismatch (got %s, meta claims %s); refusing to apply", short(got), short(rmeta.Hash))
		}
		ad.BaseHash = rmeta.Hash
		res.Applied = plaintext
		res.Hash = rmeta.Hash
		return res, nil
	default:
		return res, fmt.Errorf("internal: unexpected merge decision %v", decision)
	}
}

// DivergenceError is returned by PullConfig when local and remote both
// advanced (or there's no base anchor). It carries the hashes for a
// helpful message and is detectable via errors.As.
type DivergenceError struct {
	Decision            MergeDecision
	Local, Remote, Base string
}

func (e *DivergenceError) Error() string {
	return fmt.Sprintf("cannot pull: %s (local %s, remote %s, base %s)",
		e.Decision, short(e.Local), short(e.Remote), short(e.Base))
}

// short truncates a hex hash for display; empty stays "-".
func short(h string) string {
	if h == "" {
		return "-"
	}
	if len(h) > 12 {
		return h[:12]
	}
	return h
}
