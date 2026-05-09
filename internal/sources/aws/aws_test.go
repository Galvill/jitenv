package aws

import (
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
		"profile":           false,
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
