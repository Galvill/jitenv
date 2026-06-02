package main

// Smoke test for the JWT signing path. Forks the helper as a child
// process with a fresh-generated P-256 key, parses the resulting JWT,
// and verifies the ES256 signature against the same key. The point is
// to catch regressions in the r||s padding logic — getting DER bytes
// where raw r||s should be is a silent bug Apple would reject at
// runtime with a generic "401 Unauthorized" and no other clue.

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestJWTSignVerify(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("pkcs8: %v", err)
	}
	keyFile := filepath.Join(t.TempDir(), "k.p8")
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}

	bin := filepath.Join(t.TempDir(), "notarize-jwt")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("build: %v", err)
	}

	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(),
		"MACOS_NOTARY_KEY_FILE="+keyFile,
		"MACOS_NOTARY_KEY_ID=KID123",
		"MACOS_NOTARY_ISSUER_ID=ISS-uuid",
	)
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			t.Fatalf("run: %v (stderr=%s)", err, ee.Stderr)
		}
		t.Fatalf("run: %v", err)
	}
	jwt := strings.TrimSpace(string(out))

	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		t.Fatalf("expected 3 JWT parts, got %d: %q", len(parts), jwt)
	}

	hb, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatalf("decode header: %v", err)
	}
	var hdr map[string]string
	if err := json.Unmarshal(hb, &hdr); err != nil {
		t.Fatalf("parse header: %v", err)
	}
	if hdr["alg"] != "ES256" || hdr["kid"] != "KID123" || hdr["typ"] != "JWT" {
		t.Errorf("header mismatch: %v", hdr)
	}

	pb, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(pb, &payload); err != nil {
		t.Fatalf("parse payload: %v", err)
	}
	if payload["iss"] != "ISS-uuid" || payload["aud"] != "appstoreconnect-v1" {
		t.Errorf("payload mismatch: %v", payload)
	}

	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatalf("decode sig: %v", err)
	}
	if len(sig) != 64 {
		t.Fatalf("expected 64-byte raw ES256 signature, got %d bytes", len(sig))
	}
	r := new(big.Int).SetBytes(sig[:32])
	s := new(big.Int).SetBytes(sig[32:])

	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if !ecdsa.Verify(&key.PublicKey, sum[:], r, s) {
		t.Errorf("ES256 signature does not verify against the source key — the r||s split is broken")
	}
}
