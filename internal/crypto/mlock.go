package crypto

// LockBytes locks the page(s) backing b into RAM so the OS won't
// page them to disk under memory pressure. Best-effort: failures
// (e.g. RLIMIT_MEMLOCK on Linux, working-set limits on Windows) are
// returned as errors but the caller should NOT treat them as fatal
// — running with un-locked key material is degraded but still
// works. Pair with UnlockBytes when the key is wiped (security
// #127).
//
// The platform-split implementations live in mlock_unix.go and
// mlock_windows.go.
func LockBytes(b []byte) error {
	if len(b) == 0 {
		return nil
	}
	return lockBytes(b)
}

// UnlockBytes releases an earlier LockBytes lock on b. Always called
// in defer order alongside the zeroing of b.
func UnlockBytes(b []byte) error {
	if len(b) == 0 {
		return nil
	}
	return unlockBytes(b)
}
