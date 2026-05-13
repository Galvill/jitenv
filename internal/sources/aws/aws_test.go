package aws

import (
	"strings"
	"testing"
)

func TestNewRequiresSecretWhenAccessKeyProvided(t *testing.T) {
	if _, err := New(map[string]any{
		"region":        "us-east-1",
		"access_key_id": "AKIA...",
	}); err == nil {
		t.Fatalf("expected error when access_key_id is set without secret_access_key")
	}
}

func TestNewAcceptsStaticCredentials(t *testing.T) {
	_, err := New(map[string]any{
		"region":            "us-east-1",
		"access_key_id":     "AKIA...",
		"secret_access_key": "shh",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNewAcceptsAmbientFallback(t *testing.T) {
	_, err := New(map[string]any{
		"region": "us-east-1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSchemaHasRequiredFields(t *testing.T) {
	got := schema()
	want := map[string]bool{
		"region":            true,
		"access_key_id":     false,
		"secret_access_key": false,
		"session_token":     false,
		"role_arn":          false,
		"role_external_id":  false,
		"role_session_name": false,
		"endpoint_override": false,
	}
	if len(got) != len(want) {
		t.Fatalf("schema fields: got %d, want %d", len(got), len(want))
	}
	for _, f := range got {
		req, ok := want[f.Key]
		if !ok {
			t.Errorf("unexpected field %q", f.Key)
			continue
		}
		if f.Required != req {
			t.Errorf("field %q required: got %v, want %v", f.Key, f.Required, req)
		}
	}
}

func TestSchemaDoesNotIncludeProfile(t *testing.T) {
	for _, f := range schema() {
		if f.Key == "profile" {
			t.Fatalf("schema still exposes removed %q field", f.Key)
		}
	}
}

func TestNewRejectsLegacyProfileField(t *testing.T) {
	_, err := New(map[string]any{
		"region":  "us-east-1",
		"profile": "default",
	})
	if err == nil {
		t.Fatalf("expected error when legacy profile field is set")
	}
	const wantSub = `the "profile" field has been removed`
	if !strings.Contains(err.Error(), wantSub) {
		t.Fatalf("error message missing %q: got %q", wantSub, err.Error())
	}
	if !strings.Contains(err.Error(), "AWS_PROFILE") {
		t.Fatalf("error message should point to AWS_PROFILE: got %q", err.Error())
	}
}

func TestNewIgnoresEmptyProfileField(t *testing.T) {
	if _, err := New(map[string]any{
		"region":  "us-east-1",
		"profile": "",
	}); err != nil {
		t.Fatalf("unexpected error for empty profile: %v", err)
	}
}

func TestSchemaMarksSensitiveFields(t *testing.T) {
	sensitive := map[string]bool{
		"secret_access_key": true,
		"session_token":     true,
		"role_external_id":  true,
	}
	for _, f := range schema() {
		want := sensitive[f.Key]
		if f.Sensitive != want {
			t.Errorf("field %q sensitive: got %v, want %v", f.Key, f.Sensitive, want)
		}
	}
}
