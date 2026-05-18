package cli

import (
	"os"
	"testing"
)

// TestScrubInheritedSecretEnv asserts that the listed AWS-SDK env
// vars are removed from the agent process when scrubInheritedSecretEnv
// runs (security #122). A surviving entry would let an attacker who
// pre-set the var in the unlocking shell redirect the AWS source's
// credential chain.
func TestScrubInheritedSecretEnv(t *testing.T) {
	wantUnset := []string{
		"AWS_PROFILE",
		"AWS_SHARED_CREDENTIALS_FILE",
		"AWS_CONFIG_FILE",
		"AWS_ROLE_ARN",
		"AWS_WEB_IDENTITY_TOKEN_FILE",
		"AWS_ACCESS_KEY_ID",
		"AWS_SECRET_ACCESS_KEY",
		"AWS_SESSION_TOKEN",
	}
	for _, k := range wantUnset {
		t.Setenv(k, "attacker-chosen")
	}

	scrubInheritedSecretEnv()

	for _, k := range wantUnset {
		if v, ok := os.LookupEnv(k); ok {
			t.Errorf("%s should be unset after scrub; got %q", k, v)
		}
	}

	// Region vars are not scrubbed.
	t.Setenv("AWS_REGION", "us-east-1")
	scrubInheritedSecretEnv()
	if os.Getenv("AWS_REGION") != "us-east-1" {
		t.Error("AWS_REGION should be preserved (not a credential redirect)")
	}
}
