// Package vault implements a HashiCorp Vault KV (v1 and v2) Source.
//
// Supported auth methods (minimum viable set):
//
//   - token   — user supplies a Vault token directly (sensitive).
//   - approle — user supplies a role_id + secret_id pair (secret_id sensitive).
//
// Sensitive fields (`token`, `secret_id`) round-trip through the
// existing config envelope (`enc:v1:...`) mechanism automatically: the
// agent decrypts the params block before invoking the Constructor, so
// this package never touches plaintext encryption.
//
// KV v2 reads `<mount>/data/<name>` and unwraps `data.data`; KV v1
// reads `<mount>/<name>` directly. Non-string values in the response
// are stringified via fmt.Sprint so the returned map is always
// map[string]string and looks the same whether the secret stored an
// int, bool, or nested object.
//
// This implementation re-authenticates on every Fetch — acceptable for
// a first cut. Token renewal / lease caching is a follow-up.
package vault

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	vaultapi "github.com/hashicorp/vault/api"
	approleauth "github.com/hashicorp/vault/api/auth/approle"

	"github.com/gv/jitenv/internal/sources"
	"github.com/gv/jitenv/pkg/source"
)

const TypeName = "vault"

const (
	authToken   = "token"
	authAppRole = "approle"

	kvV1 = "v1"
	kvV2 = "v2"

	defaultMount      = "secret"
	defaultKVVersion  = kvV2
	defaultAuthMethod = authToken
)

func init() {
	sources.Register(TypeName, New)
	sources.RegisterSchema(TypeName, schema())
}

func schema() []source.ParamField {
	return []source.ParamField{
		{Key: "address", Label: "Vault address", Required: true,
			Help: "e.g. https://vault.example.com:8200"},
		{Key: "namespace", Label: "Namespace",
			Help: "Optional: Vault Enterprise namespace"},
		{Key: "auth_method", Label: "Auth method", Required: true,
			Enum: []string{authToken, authAppRole},
			Help: "token or approle"},
		{Key: "token", Label: "Vault token", Sensitive: true,
			Help: "Required when auth_method=token (encrypted at rest)."},
		{Key: "role_id", Label: "AppRole role_id",
			Help: "Required when auth_method=approle."},
		{Key: "secret_id", Label: "AppRole secret_id", Sensitive: true,
			Help: "Required when auth_method=approle (encrypted at rest)."},
		{Key: "mount", Label: "KV mount",
			Help: "Default: secret"},
		{Key: "kv_version", Label: "KV version",
			Enum: []string{kvV1, kvV2},
			Help: "v1 or v2 (default v2)"},
		{Key: "tls_skip_verify", Label: "TLS skip verify",
			Help: "Bool; for dev-mode against self-signed certs."},
	}
}

// New constructs a Vault source from a decoded (already-decrypted)
// params block.
func New(cfg map[string]any) (source.Source, error) {
	s := &vaultSource{
		address:       asString(cfg["address"]),
		namespace:     asString(cfg["namespace"]),
		authMethod:    asString(cfg["auth_method"]),
		token:         asString(cfg["token"]),
		roleID:        asString(cfg["role_id"]),
		secretID:      asString(cfg["secret_id"]),
		mount:         asString(cfg["mount"]),
		kvVersion:     asString(cfg["kv_version"]),
		tlsSkipVerify: asBool(cfg["tls_skip_verify"]),
	}
	if s.authMethod == "" {
		s.authMethod = defaultAuthMethod
	}
	if s.mount == "" {
		s.mount = defaultMount
	}
	if s.kvVersion == "" {
		s.kvVersion = defaultKVVersion
	}
	if err := s.validateStatic(); err != nil {
		return nil, err
	}
	return s, nil
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

// asBool accepts the native bool TOML produces plus the string forms
// "true" / "false" that show up when a value flows through a generic
// key/value editor.
func asBool(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		switch strings.ToLower(strings.TrimSpace(x)) {
		case "true", "1", "yes":
			return true
		}
	}
	return false
}

type vaultSource struct {
	address       string
	namespace     string
	authMethod    string
	token         string
	roleID        string
	secretID      string
	mount         string
	kvVersion     string
	tlsSkipVerify bool

	// Cached authenticated client (security #133). AppRole logins burn
	// a use-count on every call and re-send role_id + secret_id over
	// the wire, so re-authenticating per-Fetch was both wasteful and a
	// secret_id leak amplifier. The cache is invalidated when the
	// recorded lease expiry passes (with a 60s safety margin) or on
	// any auth failure the client surfaces. Static tokens have no
	// known expiry from this side; we cache the client for tokenCacheTTL
	// and let downstream API errors trigger a rebuild.
	cacheMu      sync.Mutex
	cachedClient *vaultapi.Client
	cachedExpiry time.Time
}

// tokenCacheTTL is the cache lifetime for static-token auth where we
// have no lease-duration to derive from. An hour balances "cheap" vs
// "responsive to token revocation".
const tokenCacheTTL = time.Hour

// authCacheSafetyMargin is the slack we subtract from the Vault-reported
// lease duration so we re-authenticate before the cached token actually
// expires. Hides clock skew + in-flight request latency.
const authCacheSafetyMargin = 60 * time.Second

func (v *vaultSource) Name() string { return TypeName }

func (v *vaultSource) Schema() []source.ParamField { return schema() }

// validateStatic checks the cross-field constraints that ParamField's
// per-field `Required` flag can't express. Auth-method-dependent
// fields are enforced here.
func (v *vaultSource) validateStatic() error {
	if v.address == "" {
		return fmt.Errorf("vault: address is required")
	}
	switch v.authMethod {
	case authToken:
		if v.token == "" {
			return fmt.Errorf("vault: token is required when auth_method=token")
		}
	case authAppRole:
		if v.roleID == "" {
			return fmt.Errorf("vault: role_id is required when auth_method=approle")
		}
		if v.secretID == "" {
			return fmt.Errorf("vault: secret_id is required when auth_method=approle")
		}
	default:
		return fmt.Errorf("vault: unknown auth_method %q (want %q or %q)",
			v.authMethod, authToken, authAppRole)
	}
	switch v.kvVersion {
	case kvV1, kvV2:
	default:
		return fmt.Errorf("vault: unknown kv_version %q (want %q or %q)",
			v.kvVersion, kvV1, kvV2)
	}
	return nil
}

// newClient returns an authenticated Vault client, reusing a cached
// one when it's still within its lease window (security #133). The
// cache is keyed off the source instance so two sources with the same
// address but different auth params get separate clients.
func (v *vaultSource) newClient(ctx context.Context) (*vaultapi.Client, error) {
	v.cacheMu.Lock()
	defer v.cacheMu.Unlock()

	if v.cachedClient != nil && time.Now().Before(v.cachedExpiry) {
		return v.cachedClient, nil
	}

	cli, expiry, err := v.buildAndAuthenticate(ctx)
	if err != nil {
		return nil, err
	}
	v.cachedClient = cli
	v.cachedExpiry = expiry
	return cli, nil
}

// buildAndAuthenticate constructs a fresh *vaultapi.Client and runs
// the configured auth flow. Returns the client plus the time at which
// its credentials should be treated as expired.
func (v *vaultSource) buildAndAuthenticate(ctx context.Context) (*vaultapi.Client, time.Time, error) {
	cfg := vaultapi.DefaultConfig()
	cfg.Address = v.address

	if v.tlsSkipVerify {
		// Override the transport to skip cert verification.
		// We can't rely on cfg.ConfigureTLS because mutating the
		// internal TLS config there requires a cert path; this path
		// is for dev-mode self-signed targets only.
		tr := cleanTransport()
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // dev-mode opt-in only
		cfg.HttpClient = &http.Client{Transport: tr}
	}

	cli, err := vaultapi.NewClient(cfg)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("vault: new client: %w", err)
	}
	if v.namespace != "" {
		cli.SetNamespace(v.namespace)
	}

	expiry, err := v.authenticate(ctx, cli)
	if err != nil {
		return nil, time.Time{}, err
	}
	return cli, expiry, nil
}

// cleanTransport returns a fresh http.Transport sized for one-shot
// fetches. We don't reuse api.DefaultConfig's transport here because we
// want to fully replace TLSClientConfig.
func cleanTransport() *http.Transport {
	return &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          10,
		IdleConnTimeout:       30,
		TLSHandshakeTimeout:   10,
		ExpectContinueTimeout: 1,
	}
}

// authenticate runs the configured auth flow against cli and returns
// the time at which the resulting credentials should be re-acquired.
// For static-token auth there's no server-reported expiry, so we use
// the local tokenCacheTTL ceiling; downstream API errors will force
// a rebuild sooner if the token is revoked.
func (v *vaultSource) authenticate(ctx context.Context, cli *vaultapi.Client) (time.Time, error) {
	switch v.authMethod {
	case authToken:
		cli.SetToken(v.token)
		return time.Now().Add(tokenCacheTTL), nil
	case authAppRole:
		secret := &approleauth.SecretID{FromString: v.secretID}
		appRole, err := approleauth.NewAppRoleAuth(v.roleID, secret)
		if err != nil {
			return time.Time{}, fmt.Errorf("vault: approle auth setup: %w", err)
		}
		out, err := cli.Auth().Login(ctx, appRole)
		if err != nil {
			return time.Time{}, fmt.Errorf("vault: approle login: %w", err)
		}
		if out == nil || out.Auth == nil || out.Auth.ClientToken == "" {
			return time.Time{}, fmt.Errorf("vault: approle login returned no token")
		}
		lease := time.Duration(out.Auth.LeaseDuration) * time.Second
		if lease <= authCacheSafetyMargin {
			// Server returned a near-zero lease (or 0 = no expiry).
			// Default to a short cache so we don't pin a stale token,
			// but still avoid per-Fetch re-auth.
			return time.Now().Add(authCacheSafetyMargin), nil
		}
		return time.Now().Add(lease - authCacheSafetyMargin), nil
	}
	return time.Time{}, fmt.Errorf("vault: unsupported auth_method %q", v.authMethod)
}

// Validate authenticates against Vault without reading any secret.
func (v *vaultSource) Validate(ctx context.Context) error {
	_, err := v.newClient(ctx)
	return err
}

// Fetch reads one KV path from Vault and returns a flat map of strings.
//
//	ref.ID  → path under the configured mount (e.g. "myapp/prod")
//	ref.Key → optional sub-key to pick out of the response
//
// The leading mount segment is stripped if the user passed it (so both
// "secret/myapp/prod" and "myapp/prod" work with mount="secret").
func (v *vaultSource) Fetch(ctx context.Context, ref source.SecretRef) (map[string]string, error) {
	if ref.ID == "" {
		return nil, fmt.Errorf("vault: ref.ID (KV path) is required")
	}
	cli, err := v.newClient(ctx)
	if err != nil {
		return nil, err
	}

	path := v.readPath(ref.ID)
	sec, err := cli.Logical().ReadWithContext(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("vault: read %q: %w", path, err)
	}
	if sec == nil || sec.Data == nil {
		return nil, fmt.Errorf("vault: no secret at %q", path)
	}

	data, err := v.unwrap(sec.Data)
	if err != nil {
		return nil, fmt.Errorf("vault: %s: %w", path, err)
	}

	if ref.Key != "" {
		raw, ok := data[ref.Key]
		if !ok {
			return nil, fmt.Errorf("vault: secret %q has no key %q", path, ref.Key)
		}
		return map[string]string{ref.Key: stringify(raw)}, nil
	}

	out := make(map[string]string, len(data))
	for k, val := range data {
		out[k] = stringify(val)
	}
	return out, nil
}

// readPath builds the Vault HTTP path for a KV read. For v2 the path
// shape is "<mount>/data/<name>"; for v1 it is "<mount>/<name>".
//
// If the caller passed a name that already begins with "<mount>/" we
// strip it so the same mapping works regardless of whether the user
// typed the full path or just the name under the mount.
func (v *vaultSource) readPath(name string) string {
	name = strings.TrimPrefix(name, "/")
	mountPrefix := v.mount + "/"
	if strings.HasPrefix(name, mountPrefix) {
		name = strings.TrimPrefix(name, mountPrefix)
	}
	// A v2 user occasionally types "secret/data/foo" verbatim; tolerate.
	if v.kvVersion == kvV2 && strings.HasPrefix(name, "data/") {
		name = strings.TrimPrefix(name, "data/")
	}
	if v.kvVersion == kvV2 {
		return v.mount + "/data/" + name
	}
	return v.mount + "/" + name
}

// unwrap returns the actual KV data map: for v2 that's data["data"];
// for v1 the response body is already the data map.
func (v *vaultSource) unwrap(body map[string]any) (map[string]any, error) {
	if v.kvVersion != kvV2 {
		return body, nil
	}
	raw, ok := body["data"]
	if !ok {
		return nil, fmt.Errorf("kv v2 response missing 'data' wrapper (mount %q is likely kv v1)", v.mount)
	}
	inner, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("kv v2 response 'data' field is not a map (got %T)", raw)
	}
	return inner, nil
}

// stringify converts an arbitrary JSON value to a string suitable for
// shoving through an environment variable. Nested objects and slices
// go through fmt.Sprint which produces Go-syntax output; that's a
// best-effort fallback — users storing nested data in Vault and
// expecting structured env vars should pick a specific key with
// ref.Key.
func stringify(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	default:
		return fmt.Sprint(v)
	}
}
