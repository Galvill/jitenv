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
	blob, err := SealBlob(dek, plain)
	if err != nil {
		t.Fatal(err)
	}
	if string(blob) == string(plain) {
		t.Fatal("blob is not encrypted")
	}
	got, err := OpenBlob(dek, blob)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(plain) {
		t.Fatal("blob roundtrip mismatch")
	}

	other, _ := NewDEK()
	if _, err := OpenBlob(other, blob); err == nil {
		t.Fatal("expected open with wrong DEK to fail")
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
