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
func PushConfig(ctx context.Context, adapter syncadapter.Adapter, ad *Adapter, dek, cfgBytes []byte, schemaVersion int, force bool) (PushResult, error) {
	localHash := HashConfig(cfgBytes)

	if !force {
		_, rmeta, rerr := adapter.Pull(ctx)
		switch {
		case errors.Is(rerr, syncadapters.ErrNoRemoteState):
			// first push; fine
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
