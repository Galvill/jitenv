// Signs an App Store Connect API JWT (ES256) and writes it to stdout.
// Used by notarize-poll.sh on Ubuntu so we don't need a macOS runner
// just to call `notarytool info` — the same .p8 key works against the
// REST endpoint (#226).
//
// Inputs come from env so the surrounding shell script doesn't have
// to deal with argument quoting:
//
//	MACOS_NOTARY_KEY_FILE   path to the App Store Connect .p8 (EC P-256 PEM)
//	MACOS_NOTARY_KEY_ID     key id (becomes the "kid" header claim)
//	MACOS_NOTARY_ISSUER_ID  issuer id (becomes the "iss" claim)
//
// Token TTL is 20 minutes (Apple's documented maximum). The poll
// workflow runs each tick under that, so a fresh JWT per invocation
// is fine — no caching needed.
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"time"
)

func main() {
	keyFile := mustEnv("MACOS_NOTARY_KEY_FILE")
	keyID := mustEnv("MACOS_NOTARY_KEY_ID")
	issuer := mustEnv("MACOS_NOTARY_ISSUER_ID")

	raw, err := os.ReadFile(keyFile)
	must(err)
	block, _ := pem.Decode(raw)
	if block == nil {
		die("no PEM data in %s", keyFile)
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	must(err)
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok {
		die("not an EC private key")
	}
	if key.Curve != elliptic.P256() {
		die("expected P-256 curve, got %s", key.Curve.Params().Name)
	}

	header := map[string]string{"alg": "ES256", "kid": keyID, "typ": "JWT"}
	now := time.Now().Unix()
	payload := map[string]any{
		"iss": issuer,
		"iat": now,
		"exp": now + 1200,
		"aud": "appstoreconnect-v1",
	}

	hb, err := json.Marshal(header)
	must(err)
	pb, err := json.Marshal(payload)
	must(err)
	signingInput := b64u(hb) + "." + b64u(pb)

	sum := sha256.Sum256([]byte(signingInput))
	r, s, err := ecdsa.Sign(rand.Reader, key, sum[:])
	must(err)

	// JWS ES256 wants the fixed-width concatenation r||s (32+32
	// bytes), NOT the ASN.1-DER form openssl-dgst would emit. Pad
	// each scalar to 32 bytes big-endian.
	sig := make([]byte, 64)
	rBytes := r.Bytes()
	sBytes := s.Bytes()
	copy(sig[32-len(rBytes):32], rBytes)
	copy(sig[64-len(sBytes):], sBytes)

	fmt.Println(signingInput + "." + base64.RawURLEncoding.EncodeToString(sig))
}

func b64u(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func mustEnv(k string) string {
	v := os.Getenv(k)
	if v == "" {
		die("env %s required", k)
	}
	return v
}

func must(err error) {
	if err != nil {
		die("%v", err)
	}
}

func die(f string, a ...any) {
	fmt.Fprintf(os.Stderr, "notarize-jwt: "+f+"\n", a...)
	os.Exit(1)
}
