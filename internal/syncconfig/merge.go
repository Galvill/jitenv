package syncconfig

// MergeDecision is the outcome of comparing local, remote, and the
// recorded base snapshot for one adapter. v1 implements whole-file
// last-writer-wins with a divergence fence (issue #241, merge model
// (i)): cell-level / 3-way mapping merge is explicitly deferred because
// mapping declaration order is load-bearing and a stable Mapping.ID is
// out of scope.
type MergeDecision int

const (
	// DecideNoop: remote == local (or remote unchanged since base and
	// local unchanged since base). Nothing to do.
	DecideNoop MergeDecision = iota
	// DecideFastForward: local is unchanged since base, remote advanced.
	// Safe to write remote -> local.
	DecideFastForward
	// DecidePushNeeded: remote is unchanged since base, local advanced.
	// On pull this is a no-op (local is ahead); status reports it.
	DecidePushNeeded
	// DecideDiverged: BOTH local and remote advanced past base. v1
	// aborts and tells the user to reconcile manually.
	DecideDiverged
	// DecideNoRemote: the remote has no state yet (first push pending).
	DecideNoRemote
	// DecideNoBase: no base snapshot recorded yet (adapter never
	// synced), but the remote DOES have state. Treated as divergence:
	// we can't prove local descends from remote, so refuse to clobber.
	DecideNoBase
)

func (d MergeDecision) String() string {
	switch d {
	case DecideNoop:
		return "up-to-date"
	case DecideFastForward:
		return "behind (pull will fast-forward)"
	case DecidePushNeeded:
		return "ahead (push to publish local changes)"
	case DecideDiverged:
		return "diverged (local and remote both changed)"
	case DecideNoRemote:
		return "no remote state yet (push to publish)"
	case DecideNoBase:
		return "no local base snapshot (pull blocked; reconcile manually)"
	default:
		return "unknown"
	}
}

// Decide compares the three snapshot hashes. localHash is the SHA-256 of
// the current on-disk config.toml; remoteHash is Meta.Hash from the
// pulled blob; baseHash is what the sidecar recorded at the last sync
// against this adapter. remotePresent is false when Pull returned
// ErrNoRemoteState.
//
// The decision is purely hash-based — no plaintext is needed — so
// `sync status` can report divergence without decrypting anything.
func Decide(localHash, remoteHash, baseHash string, remotePresent bool) MergeDecision {
	if !remotePresent {
		return DecideNoRemote
	}
	if localHash == remoteHash {
		return DecideNoop
	}
	if baseHash == "" {
		// Remote exists, local differs from it, and we have no proof
		// local was derived from remote. Refuse to fast-forward (would
		// silently clobber) and refuse to call it clean.
		return DecideNoBase
	}
	localChanged := localHash != baseHash
	remoteChanged := remoteHash != baseHash
	switch {
	case !localChanged && remoteChanged:
		return DecideFastForward
	case localChanged && !remoteChanged:
		return DecidePushNeeded
	case localChanged && remoteChanged:
		return DecideDiverged
	default:
		// !localChanged && !remoteChanged but hashes differ is
		// impossible (both equal base => equal each other), handled by
		// the Noop check above. Fall through defensively.
		return DecideNoop
	}
}
