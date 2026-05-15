package cli

import (
	"log/slog"
	"os"
)

// awsScrubVars lists the AWS-SDK env vars that the agent must NOT
// inherit from the unlocking shell (security #122). Each one can
// redirect the SDK's default credential chain at the file-path
// level: AWS_PROFILE picks a section out of an attacker-controlled
// credentials file; AWS_SHARED_CREDENTIALS_FILE / AWS_CONFIG_FILE
// supply the file directly; AWS_ROLE_ARN +
// AWS_WEB_IDENTITY_TOKEN_FILE drive IRSA / OIDC assumption against
// an attacker-chosen token path. Users who want these honoured
// should encode them as explicit fields under [sources.<name>.params]
// where they round-trip through the encrypted config.
//
// Region-only vars (AWS_REGION / AWS_DEFAULT_REGION) are not
// scrubbed: they don't influence which credentials are loaded.
var awsScrubVars = []string{
	"AWS_PROFILE",
	"AWS_SHARED_CREDENTIALS_FILE",
	"AWS_CONFIG_FILE",
	"AWS_ROLE_ARN",
	"AWS_WEB_IDENTITY_TOKEN_FILE",
	"AWS_ROLE_SESSION_NAME",
	"AWS_ACCESS_KEY_ID",
	"AWS_SECRET_ACCESS_KEY",
	"AWS_SESSION_TOKEN",
}

// scrubInheritedSecretEnv removes credential-chain env vars from the
// agent process so they can't poison the SDK's default lookup. Called
// once at __agent startup, before any source.Constructor runs.
//
// If any var was actually present, a WARN line names it so an operator
// who relied on env-based config can see why the agent stopped
// picking those creds up.
func scrubInheritedSecretEnv() {
	var hit []string
	for _, k := range awsScrubVars {
		if _, ok := os.LookupEnv(k); ok {
			hit = append(hit, k)
			_ = os.Unsetenv(k)
		}
	}
	if len(hit) > 0 {
		slog.Warn("scrubbed AWS env vars at agent startup; encode them as source params instead (security #122)",
			"unset", hit)
	}
}
