package syncconfig

import (
	"encoding/base64"
	"path/filepath"
	"testing"

	"github.com/gv/jitenv/internal/crypto"
)

func newTestFile(t *testing.T) *File {
	t.Helper()
	salt, err := crypto.NewSalt()
	if err != nil {
		t.Fatal(err)
	}
	p := crypto.DefaultArgonParams()
	f := &File{
		Version:        Version,
		Salt:           base64.StdEncoding.EncodeToString(salt),
		ArgonTime:      p.Time,
		ArgonMemoryKiB: p.MemKiB,
		ArgonThreads:   p.Threads,
	}
	return f
}

func TestDEKWrapUnwrapRoundTrip(t *testing.T) {
	f := newTestFile(t)
	mk, err := f.DeriveMasterKey([]byte("correct horse battery staple"))
	if err != nil {
		t.Fatal(err)
	}
	dek, err := NewDEK()
	if err != nil {
		t.Fatal(err)
	}
	if err := f.WrapDEK(mk, dek); err != nil {
		t.Fatal(err)
	}
	got, err := f.UnwrapDEK(mk)
	if err != nil {
		t.Fatalf("unwrap: %v", err)
	}
	if string(got) != string(dek) {
		t.Fatal("DEK roundtrip mismatch")
	}
}

// TestUnwrapDEKReadsLegacyStringPathWrap is a regression for issue #277:
// WrapDEK now uses crypto.EncryptFieldBytes to keep the DEK off the Go
// string heap, but the on-disk envelope format is identical to what the
// previous string-typed path produced. Existing sync.toml files written
// by older builds must still unwrap with the bytes-path UnwrapDEK.
func TestUnwrapDEKReadsLegacyStringPathWrap(t *testing.T) {
	f := newTestFile(t)
	mk, err := f.DeriveMasterKey([]byte("correct horse battery staple"))
	if err != nil {
		t.Fatal(err)
	}
	dek, err := NewDEK()
	if err != nil {
		t.Fatal(err)
	}
	// Simulate the OLD wrap path explicitly: string(dek) -> EncryptField.
	env, err := crypto.EncryptField(mk, string(dek), dekWrapAAD)
	if err != nil {
		t.Fatal(err)
	}
	f.WrappedDEK = env

	got, err := f.UnwrapDEK(mk)
	if err != nil {
		t.Fatalf("unwrap of legacy-shaped envelope: %v", err)
	}
	if string(got) != string(dek) {
		t.Fatal("legacy-shaped DEK roundtrip mismatch")
	}
}

func TestUnwrapWrongPassphraseFailsClosed(t *testing.T) {
	f := newTestFile(t)
	mk, _ := f.DeriveMasterKey([]byte("right"))
	dek, _ := NewDEK()
	if err := f.WrapDEK(mk, dek); err != nil {
		t.Fatal(err)
	}

	wrongMK, _ := f.DeriveMasterKey([]byte("wrong"))
	if _, err := f.UnwrapDEK(wrongMK); err == nil {
		t.Fatal("expected unwrap with wrong passphrase to fail")
	}
}

func TestBlobSealOpenRoundTrip(t *testing.T) {
	dek, _ := NewDEK()
	plain := []byte("version = 1\n[_meta]\nsalt = \"x\"\n")
	hash := HashConfig(plain)
	blob, err := SealBlob(dek, plain, hash)
	if err != nil {
		t.Fatal(err)
	}
	if string(blob) == string(plain) {
		t.Fatal("blob is not encrypted")
	}
	got, err := OpenBlob(dek, blob, hash)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(plain) {
		t.Fatal("blob roundtrip mismatch")
	}

	other, _ := NewDEK()
	if _, err := OpenBlob(other, blob, hash); err == nil {
		t.Fatal("expected open with wrong DEK to fail")
	}
}

// TestBlobAADBindsMetaHash proves the meta hash is authenticated into the
// blob's AEAD associated data: opening with a different (tampered or
// swapped) meta hash fails the tag check even with the correct DEK.
func TestBlobAADBindsMetaHash(t *testing.T) {
	dek, _ := NewDEK()
	plain := []byte("version = 1\n# real\n")
	hash := HashConfig(plain)
	blob, err := SealBlob(dek, plain, hash)
	if err != nil {
		t.Fatal(err)
	}

	// A storage attacker swaps in a different meta hash (e.g. from a
	// prior push) while keeping the blob. OpenBlob must reject it.
	tampered := HashConfig([]byte("version = 1\n# attacker swap\n"))
	if _, err := OpenBlob(dek, blob, tampered); err == nil {
		t.Fatal("expected open with tampered meta hash to fail (AAD not bound)")
	}
	// Empty/zeroed meta hash must also fail.
	if _, err := OpenBlob(dek, blob, ""); err == nil {
		t.Fatal("expected open with empty meta hash to fail")
	}
	// Sanity: the correct hash still opens.
	if _, err := OpenBlob(dek, blob, hash); err != nil {
		t.Fatalf("correct meta hash should open: %v", err)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	f := newTestFile(t)
	mk, _ := f.DeriveMasterKey([]byte("pw"))
	dek, _ := NewDEK()
	if err := f.WrapDEK(mk, dek); err != nil {
		t.Fatal(err)
	}
	ad := Adapter{Name: "home", Type: "file", Params: map[string]any{"path": "/tmp/x", "secret": "hunter2"}}
	if err := EncryptParams(mk, &ad); err != nil {
		t.Fatal(err)
	}
	if ad.Params["secret"] == "hunter2" {
		t.Fatal("secret param not encrypted")
	}
	f.Adapters = append(f.Adapters, ad)

	path := filepath.Join(t.TempDir(), "sync.toml")
	if err := Save(path, f); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	la, _, ok := loaded.FindAdapter("home")
	if !ok {
		t.Fatal("adapter not found after reload")
	}
	dp, err := DecryptParams(mk, la)
	if err != nil {
		t.Fatal(err)
	}
	if dp["secret"] != "hunter2" || dp["path"] != "/tmp/x" {
		t.Fatalf("decrypted params wrong: %v", dp)
	}
}

func TestDecideMatrix(t *testing.T) {
	cases := []struct {
		name                string
		local, remote, base string
		present             bool
		want                MergeDecision
	}{
		{"no remote", "L", "", "", false, DecideNoRemote},
		{"equal", "X", "X", "X", true, DecideNoop},
		{"equal no base", "X", "X", "", true, DecideNoop},
		{"fast-forward", "B", "R", "B", true, DecideFastForward},
		{"push needed", "L", "B", "B", true, DecidePushNeeded},
		{"diverged", "L", "R", "B", true, DecideDiverged},
		{"no base remote present differs", "L", "R", "", true, DecideNoBase},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Decide(c.local, c.remote, c.base, c.present)
			if got != c.want {
				t.Fatalf("Decide(%q,%q,%q,%v) = %v, want %v", c.local, c.remote, c.base, c.present, got, c.want)
			}
		})
	}
}
