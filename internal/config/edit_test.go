package config

import (
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestDeriveKeyFromMeta_RejectsWeakArgonParams is the regression for
// security #111: the KDF parameters in [_meta] are stored unauthenticated
// on disk. An attacker who can write to config.toml could otherwise drop
// argon_time to 1 and argon_memory_kib to a few KiB so the next derive
// (and any offline attempt against a leaked memory/swap dump) costs
// almost nothing. Reject configs whose params are below documented
// floors at derive time.
func TestDeriveKeyFromMeta_RejectsWeakArgonParams(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	pw := []byte("hunter2")
	if err := InitNew(path, pw); err != nil {
		t.Fatalf("init: %v", err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	// Sanity: default-init params must derive cleanly.
	if _, err := DeriveKeyFromMeta(c, pw); err != nil {
		t.Fatalf("default params should derive: %v", err)
	}

	cases := []struct {
		name string
		mut  func(*Meta)
		want string
	}{
		{"time too low", func(m *Meta) { m.ArgonTime = 1 }, "argon_time"},
		{"memory too low", func(m *Meta) { m.ArgonMemoryKiB = 8 }, "argon_memory"},
		{"threads zero would be replaced by default", nil, ""}, // sanity entry; threads=0 is preserved as default via nzU8
	}
	for _, tc := range cases {
		if tc.mut == nil {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			// Reload fresh each iteration so previous mutations don't
			// taint the next subtest.
			c2, err := Load(path)
			if err != nil {
				t.Fatalf("reload: %v", err)
			}
			tc.mut(&c2.Meta)
			_, err = DeriveKeyFromMeta(c2, pw)
			if err == nil {
				t.Fatalf("expected error for weak %s, got nil", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q missing expected mention of %q", err.Error(), tc.want)
			}
		})
	}
}

func TestInitAndDeriveKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	pw := []byte("hunter2")

	if err := InitNew(path, pw); err != nil {
		t.Fatalf("init: %v", err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.Meta.Salt == "" || c.Meta.Verify == "" {
		t.Fatalf("expected meta to be populated: %+v", c.Meta)
	}

	key, err := DeriveKeyFromMeta(c, pw)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	defer zero(key)

	// #326: the wrong-passphrase return must be the exported sentinel so
	// the bounded-retry helper in internal/unlock can recognise it via
	// errors.Is. The user-facing message stays unchanged ("incorrect
	// passphrase") so cobra's exit-1 output is identical to pre-#326.
	if _, err := DeriveKeyFromMeta(c, []byte("wrong")); err == nil {
		t.Fatalf("expected wrong passphrase to fail")
	} else {
		if !errors.Is(err, ErrIncorrectPassphrase) {
			t.Errorf("expected errors.Is(err, ErrIncorrectPassphrase), got %v", err)
		}
		if err.Error() != "incorrect passphrase" {
			t.Errorf("user-facing error copy regressed; want %q got %q", "incorrect passphrase", err.Error())
		}
	}

	if err := InitNew(path, pw); err == nil {
		t.Fatalf("expected init to refuse overwriting existing file")
	}
}

func TestResolveDefaultPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		// Hardcoded Unix-style "/tmp/..." paths get rewritten by
		// filepath.Join on Windows to use backslashes, so the literal
		// string comparison fails. Real Windows path resolution lives
		// behind #39 stage 2+; for now skip rather than rewrite the
		// expected value, which would weaken the Unix assertion.
		t.Skip("windows: Unix-style /tmp paths in fixtures; tracking in #39")
	}
	t.Setenv("JITENV_CONFIG", "/tmp/explicit.toml")
	got, err := Resolve("")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != "/tmp/explicit.toml" {
		t.Fatalf("expected env override, got %q", got)
	}

	t.Setenv("JITENV_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdg")
	got, _ = Resolve("")
	if got != "/tmp/xdg/jitenv/config.toml" {
		t.Fatalf("expected XDG path, got %q", got)
	}
}
