package vault

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gv/jitenv/internal/crypto"
	"github.com/gv/jitenv/pkg/source"
)

// ---------- Validate() / construction matrix ----------

func TestNew_TokenAuth_OK(t *testing.T) {
	s, err := New(map[string]any{
		"address":     "http://127.0.0.1:1",
		"auth_method": "token",
		"token":       "root",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Name() != "vault" {
		t.Fatalf("Name() = %q, want vault", s.Name())
	}
}

func TestNew_TokenAuth_MissingToken(t *testing.T) {
	_, err := New(map[string]any{
		"address":     "http://127.0.0.1:1",
		"auth_method": "token",
	})
	if err == nil || !strings.Contains(err.Error(), "token is required") {
		t.Fatalf("expected token-required error, got %v", err)
	}
}

func TestNew_AppRole_OK(t *testing.T) {
	_, err := New(map[string]any{
		"address":     "http://127.0.0.1:1",
		"auth_method": "approle",
		"role_id":     "rid",
		"secret_id":   "sid",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNew_AppRole_MissingRoleID(t *testing.T) {
	_, err := New(map[string]any{
		"address":     "http://127.0.0.1:1",
		"auth_method": "approle",
		"secret_id":   "sid",
	})
	if err == nil || !strings.Contains(err.Error(), "role_id is required") {
		t.Fatalf("expected role_id-required error, got %v", err)
	}
}

func TestNew_AppRole_MissingSecretID(t *testing.T) {
	_, err := New(map[string]any{
		"address":     "http://127.0.0.1:1",
		"auth_method": "approle",
		"role_id":     "rid",
	})
	if err == nil || !strings.Contains(err.Error(), "secret_id is required") {
		t.Fatalf("expected secret_id-required error, got %v", err)
	}
}

func TestNew_AddressRequired(t *testing.T) {
	_, err := New(map[string]any{
		"auth_method": "token",
		"token":       "x",
	})
	if err == nil || !strings.Contains(err.Error(), "address is required") {
		t.Fatalf("expected address-required error, got %v", err)
	}
}

func TestNew_UnknownAuthMethod(t *testing.T) {
	_, err := New(map[string]any{
		"address":     "http://127.0.0.1:1",
		"auth_method": "kubernetes",
		"token":       "x",
	})
	if err == nil || !strings.Contains(err.Error(), "unknown auth_method") {
		t.Fatalf("expected unknown auth_method error, got %v", err)
	}
}

func TestNew_UnknownKVVersion(t *testing.T) {
	_, err := New(map[string]any{
		"address":     "http://127.0.0.1:1",
		"auth_method": "token",
		"token":       "x",
		"kv_version":  "v3",
	})
	if err == nil || !strings.Contains(err.Error(), "unknown kv_version") {
		t.Fatalf("expected unknown kv_version error, got %v", err)
	}
}

// TestNew_TLSSkipVerify_LogsWarning is the regression for security #113:
// enabling tls_skip_verify must produce a loud, structured warning at
// New() time so the operator can spot a misconfigured Vault source in
// the agent log. Without this signal, a stray dev-mode setting can
// survive a promotion to production and MITM all Vault fetches via
// HTTPS_PROXY.
func TestNew_TLSSkipVerify_LogsWarning(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	if _, err := New(map[string]any{
		"address":         "https://vault.example.com:8200",
		"auth_method":     "token",
		"token":           "x",
		"tls_skip_verify": true,
	}); err != nil {
		t.Fatalf("New: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, `"level":"WARN"`) {
		t.Errorf("expected a WARN-level log line; got:\n%s", out)
	}
	if !strings.Contains(out, "tls_skip_verify") && !strings.Contains(out, "TLS verification disabled") {
		t.Errorf("warning should mention TLS verification being disabled; got:\n%s", out)
	}

	// Reset the buffer; a source without the flag must NOT log a
	// warning, otherwise every legitimate Vault setup spams the log.
	buf.Reset()
	if _, err := New(map[string]any{
		"address":     "https://vault.example.com:8200",
		"auth_method": "token",
		"token":       "x",
	}); err != nil {
		t.Fatalf("New (clean): %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("unexpected warning when tls_skip_verify is unset:\n%s", buf.String())
	}
}

func TestNew_DefaultsApplied(t *testing.T) {
	s, err := New(map[string]any{
		"address":     "http://127.0.0.1:1",
		"auth_method": "token",
		"token":       "x",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	vs := s.(*vaultSource)
	if vs.mount != "secret" {
		t.Errorf("mount default: got %q, want secret", vs.mount)
	}
	if vs.kvVersion != "v2" {
		t.Errorf("kv_version default: got %q, want v2", vs.kvVersion)
	}
}

// ---------- Schema metadata ----------

func TestSchema_HasExpectedFields(t *testing.T) {
	got := schema()
	want := map[string]struct {
		required  bool
		sensitive bool
	}{
		"address":         {required: true, sensitive: false},
		"namespace":       {required: false, sensitive: false},
		"auth_method":     {required: true, sensitive: false},
		"token":           {required: false, sensitive: true},
		"role_id":         {required: false, sensitive: false},
		"secret_id":       {required: false, sensitive: true},
		"mount":           {required: false, sensitive: false},
		"kv_version":      {required: false, sensitive: false},
		"tls_skip_verify": {required: false, sensitive: false},
	}
	if len(got) != len(want) {
		t.Fatalf("schema fields: got %d, want %d", len(got), len(want))
	}
	for _, f := range got {
		w, ok := want[f.Key]
		if !ok {
			t.Errorf("unexpected field %q", f.Key)
			continue
		}
		if f.Required != w.required {
			t.Errorf("field %q required: got %v, want %v", f.Key, f.Required, w.required)
		}
		if f.Sensitive != w.sensitive {
			t.Errorf("field %q sensitive: got %v, want %v", f.Key, f.Sensitive, w.sensitive)
		}
	}
}

// ---------- readPath / unwrap unit logic ----------

func TestReadPath_V2_PlainName(t *testing.T) {
	v := &vaultSource{mount: "secret", kvVersion: "v2"}
	if got := v.readPath("myapp/prod"); got != "secret/data/myapp/prod" {
		t.Fatalf("got %q", got)
	}
}

func TestReadPath_V2_StripsMountPrefix(t *testing.T) {
	v := &vaultSource{mount: "secret", kvVersion: "v2"}
	if got := v.readPath("secret/myapp/prod"); got != "secret/data/myapp/prod" {
		t.Fatalf("got %q", got)
	}
}

func TestReadPath_V2_StripsExplicitDataSegment(t *testing.T) {
	v := &vaultSource{mount: "secret", kvVersion: "v2"}
	if got := v.readPath("data/myapp/prod"); got != "secret/data/myapp/prod" {
		t.Fatalf("got %q", got)
	}
}

func TestReadPath_V1(t *testing.T) {
	v := &vaultSource{mount: "kv", kvVersion: "v1"}
	if got := v.readPath("myapp/prod"); got != "kv/myapp/prod" {
		t.Fatalf("got %q", got)
	}
}

// TestFetch_RejectsPathTraversal is the regression for security #119:
// a VarRef.Ref containing ".." segments would otherwise let a user
// who controls the config escape the configured mount and reach any
// Vault path their token's policy permits (e.g. ../../sys/policies).
// Fetch must reject such refs before issuing the HTTP request.
func TestFetch_RejectsPathTraversal(t *testing.T) {
	s, err := New(map[string]any{
		"address":     "http://127.0.0.1:1",
		"auth_method": "token",
		"token":       "x",
		"mount":       "secret",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cases := []string{
		"../../sys/policies/acl/default",
		"foo/../../etc",
		"foo/..",
		"..",
		"./.././bar",
	}
	for _, name := range cases {
		// Cancelled context proves we reject BEFORE the network: an
		// implementation that defers validation would either return
		// context.Canceled (network attempted) or block. Our reject
		// returns a synchronous "traversal"-message error.
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := s.Fetch(ctx, source.SecretRef{ID: name})
		if err == nil {
			t.Errorf("Fetch(%q) must reject path-traversal ref", name)
			continue
		}
		if !strings.Contains(err.Error(), "traversal") {
			t.Errorf("Fetch(%q) error %q should mention 'traversal' (rejection at validation, not network failure)", name, err)
		}
	}
}

func TestUnwrap_V2_MissingDataWrapper(t *testing.T) {
	v := &vaultSource{kvVersion: "v2"}
	_, err := v.unwrap(map[string]any{"foo": "bar"})
	if err == nil || !strings.Contains(err.Error(), "kv v2") {
		t.Fatalf("expected kv v2 error, got %v", err)
	}
}

// ---------- Stringify ----------

func TestStringify(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{"hello", "hello"},
		{true, "true"},
		{false, "false"},
		{42, "42"},
		{3.14, "3.14"},
		{nil, ""},
		{map[string]any{"a": 1}, "map[a:1]"},
	}
	for _, c := range cases {
		if got := stringify(c.in); got != c.want {
			t.Errorf("stringify(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

// ---------- Fetch against a stubbed Vault HTTP server ----------

// stubServer returns an httptest.Server that mimics Vault's KV
// endpoints. routes is consulted for "<method> <path>" lookups.
func stubServer(t *testing.T, routes map[string]http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Method + " " + r.URL.Path
		h, ok := routes[key]
		if !ok {
			t.Logf("stubServer: no handler for %s", key)
			http.Error(w, "no handler: "+key, http.StatusNotFound)
			return
		}
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func TestFetch_TokenAuth_V2_FlatStrings(t *testing.T) {
	srv := stubServer(t, map[string]http.HandlerFunc{
		"GET /v1/secret/data/myapp/prod": func(w http.ResponseWriter, r *http.Request) {
			if got := r.Header.Get("X-Vault-Token"); got != "root" {
				t.Errorf("token header: got %q, want root", got)
			}
			writeJSON(w, 200, map[string]any{
				"data": map[string]any{
					"data": map[string]any{
						"DB_URL":   "postgres://x",
						"API_KEY":  "abc",
						"replicas": 3,
						"debug":    true,
					},
					"metadata": map[string]any{"version": 1},
				},
			})
		},
	})

	s, err := New(map[string]any{
		"address":     srv.URL,
		"auth_method": "token",
		"token":       "root",
		"kv_version":  "v2",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got, err := s.Fetch(context.Background(), source.SecretRef{ID: "myapp/prod"})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	want := map[string]string{
		"DB_URL":   "postgres://x",
		"API_KEY":  "abc",
		"replicas": "3",
		"debug":    "true",
	}
	if !mapEqual(got, want) {
		t.Fatalf("Fetch: got %v, want %v", got, want)
	}
}

func TestFetch_TokenAuth_V1(t *testing.T) {
	srv := stubServer(t, map[string]http.HandlerFunc{
		"GET /v1/kv/myapp/prod": func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, 200, map[string]any{
				"data": map[string]any{
					"DB_URL": "postgres://x",
				},
			})
		},
	})

	s, err := New(map[string]any{
		"address":     srv.URL,
		"auth_method": "token",
		"token":       "root",
		"mount":       "kv",
		"kv_version":  "v1",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got, err := s.Fetch(context.Background(), source.SecretRef{ID: "myapp/prod"})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got["DB_URL"] != "postgres://x" {
		t.Fatalf("Fetch: got %v", got)
	}
}

func TestFetch_AppRoleAuth_V2(t *testing.T) {
	var loginCalls atomic.Int32
	srv := stubServer(t, map[string]http.HandlerFunc{
		"PUT /v1/auth/approle/login": func(w http.ResponseWriter, r *http.Request) {
			loginCalls.Add(1)
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("approle login body: %v", err)
			}
			if body["role_id"] != "rid" || body["secret_id"] != "sid" {
				t.Errorf("approle creds: got %v", body)
			}
			writeJSON(w, 200, map[string]any{
				"auth": map[string]any{
					"client_token":   "issued-token",
					"lease_duration": 3600,
				},
			})
		},
		"GET /v1/secret/data/myapp/prod": func(w http.ResponseWriter, r *http.Request) {
			if got := r.Header.Get("X-Vault-Token"); got != "issued-token" {
				t.Errorf("token header on KV read: got %q, want issued-token", got)
			}
			writeJSON(w, 200, map[string]any{
				"data": map[string]any{
					"data": map[string]any{"FOO": "bar"},
				},
			})
		},
	})

	s, err := New(map[string]any{
		"address":     srv.URL,
		"auth_method": "approle",
		"role_id":     "rid",
		"secret_id":   "sid",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got, err := s.Fetch(context.Background(), source.SecretRef{ID: "myapp/prod"})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got["FOO"] != "bar" {
		t.Fatalf("Fetch: got %v", got)
	}
	if loginCalls.Load() < 1 {
		t.Fatalf("expected at least one approle login call, got %d", loginCalls.Load())
	}
}

func TestFetch_MissingSecret_V2(t *testing.T) {
	srv := stubServer(t, map[string]http.HandlerFunc{
		"GET /v1/secret/data/missing": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		},
	})
	s, err := New(map[string]any{
		"address":     srv.URL,
		"auth_method": "token",
		"token":       "root",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = s.Fetch(context.Background(), source.SecretRef{ID: "missing"})
	if err == nil {
		t.Fatalf("expected error for missing secret")
	}
	// The Vault client returns nil, nil on 404 for some KV paths; both
	// "no secret at" and a wrapped HTTP error are acceptable, just so
	// long as the caller gets *some* error.
	if !strings.Contains(err.Error(), "vault") {
		t.Fatalf("error should mention vault: %v", err)
	}
}

func TestFetch_PicksKey_V2(t *testing.T) {
	srv := stubServer(t, map[string]http.HandlerFunc{
		"GET /v1/secret/data/myapp": func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, 200, map[string]any{
				"data": map[string]any{
					"data": map[string]any{
						"DB_URL":  "postgres://x",
						"API_KEY": "abc",
					},
				},
			})
		},
	})
	s, err := New(map[string]any{
		"address":     srv.URL,
		"auth_method": "token",
		"token":       "root",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got, err := s.Fetch(context.Background(), source.SecretRef{ID: "myapp", Key: "API_KEY"})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(got) != 1 || got["API_KEY"] != "abc" {
		t.Fatalf("Fetch key-pick: got %v", got)
	}
}

func TestFetch_RefIDRequired(t *testing.T) {
	s, err := New(map[string]any{
		"address":     "http://127.0.0.1:1",
		"auth_method": "token",
		"token":       "root",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = s.Fetch(context.Background(), source.SecretRef{})
	if err == nil || !strings.Contains(err.Error(), "ref.ID") {
		t.Fatalf("expected ref.ID error, got %v", err)
	}
}

// ---------- enc:v1: envelope round-trip ----------

// TestEnvelopeRoundTripIntoNew confirms that values supplied as
// `enc:v1:...` envelopes are decoded before the Vault Source sees
// them. The agent's resolver calls DecryptStringsInPlace on the
// params block, so we replicate that here.
func TestEnvelopeRoundTripIntoNew(t *testing.T) {
	salt, err := crypto.NewSalt()
	if err != nil {
		t.Fatalf("NewSalt: %v", err)
	}
	key := crypto.DeriveKey([]byte("correct horse battery staple"), salt, crypto.DefaultArgonParams())
	tokenEnv, err := crypto.EncryptField(key, "real-token")
	if err != nil {
		t.Fatalf("EncryptField: %v", err)
	}
	secretEnv, err := crypto.EncryptField(key, "real-secret-id")
	if err != nil {
		t.Fatalf("EncryptField: %v", err)
	}
	if !crypto.IsEnvelope(tokenEnv) || !crypto.IsEnvelope(secretEnv) {
		t.Fatalf("envelopes not formed as enc:v1:")
	}

	// Token-auth envelope round-trip.
	params := map[string]any{
		"address":     "http://127.0.0.1:1",
		"auth_method": "token",
		"token":       tokenEnv,
	}
	if err := crypto.DecryptStringsInPlace(key, params); err != nil {
		t.Fatalf("DecryptStringsInPlace: %v", err)
	}
	if params["token"] != "real-token" {
		t.Fatalf("token after decrypt: got %v", params["token"])
	}
	if _, err := New(params); err != nil {
		t.Fatalf("New after decrypt: %v", err)
	}

	// AppRole envelope round-trip.
	params2 := map[string]any{
		"address":     "http://127.0.0.1:1",
		"auth_method": "approle",
		"role_id":     "rid",
		"secret_id":   secretEnv,
	}
	if err := crypto.DecryptStringsInPlace(key, params2); err != nil {
		t.Fatalf("DecryptStringsInPlace: %v", err)
	}
	if params2["secret_id"] != "real-secret-id" {
		t.Fatalf("secret_id after decrypt: got %v", params2["secret_id"])
	}
	if _, err := New(params2); err != nil {
		t.Fatalf("New after decrypt (approle): %v", err)
	}
}

// ---------- helpers ----------

func mapEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
