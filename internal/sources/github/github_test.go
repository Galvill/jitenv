package github

import (
	"context"
	"strings"
	"testing"

	"github.com/gv/jitenv/pkg/source"
)

func TestNewRequiresToken(t *testing.T) {
	if _, err := New(map[string]any{}); err == nil {
		t.Fatalf("expected error when token is missing")
	}
}

func TestNewAcceptsToken(t *testing.T) {
	s, err := New(map[string]any{"token": "ghp_..."})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Name() != TypeName {
		t.Errorf("Name(): got %q, want %q", s.Name(), TypeName)
	}
}

func TestSchemaShape(t *testing.T) {
	got := schema()
	want := map[string]struct {
		Required  bool
		Sensitive bool
	}{
		"token":   {Required: true, Sensitive: true},
		"api_url": {},
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
		if f.Required != w.Required {
			t.Errorf("field %q required: got %v, want %v", f.Key, f.Required, w.Required)
		}
		if f.Sensitive != w.Sensitive {
			t.Errorf("field %q sensitive: got %v, want %v", f.Key, f.Sensitive, w.Sensitive)
		}
	}
}

func TestFetchRequiresKey(t *testing.T) {
	s, err := New(map[string]any{"token": "ghp_..."})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, err = s.Fetch(context.Background(), source.SecretRef{ID: "owner/repo"})
	if err == nil || !strings.Contains(err.Error(), "ref.Key") {
		t.Fatalf("expected ref.Key error, got %v", err)
	}
}

func TestFetchRejectsSecretScope(t *testing.T) {
	s, err := New(map[string]any{"token": "ghp_..."})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, err = s.Fetch(context.Background(), source.SecretRef{
		ID:    "owner/repo",
		Key:   "FOO",
		Extra: map[string]string{"scope": "secret"},
	})
	if err == nil || !strings.Contains(err.Error(), "secret values are not retrievable") {
		t.Fatalf("expected secret-not-retrievable error, got %v", err)
	}
}

func TestFetchValidatesRepoScopeID(t *testing.T) {
	s, err := New(map[string]any{"token": "ghp_..."})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, err = s.Fetch(context.Background(), source.SecretRef{
		ID:    "not-owner-slash-repo",
		Key:   "FOO",
		Extra: map[string]string{"scope": "repo"},
	})
	if err == nil || !strings.Contains(err.Error(), "owner/repo") {
		t.Fatalf("expected owner/repo error, got %v", err)
	}
}

func TestFetchValidatesEnvScopeRequiresEnvironment(t *testing.T) {
	s, err := New(map[string]any{"token": "ghp_..."})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, err = s.Fetch(context.Background(), source.SecretRef{
		ID:    "owner/repo",
		Key:   "FOO",
		Extra: map[string]string{"scope": "env"},
	})
	if err == nil || !strings.Contains(err.Error(), "environment") {
		t.Fatalf("expected environment error, got %v", err)
	}
}

func TestSplitOwnerRepo(t *testing.T) {
	cases := []struct {
		in    string
		owner string
		repo  string
		ok    bool
	}{
		{"owner/repo", "owner", "repo", true},
		{"a/b/c", "a", "b/c", true}, // SplitN keeps the rest as repo
		{"owner", "", "", false},
		{"/repo", "", "", false},
		{"owner/", "", "", false},
		{"", "", "", false},
	}
	for _, c := range cases {
		o, r, ok := splitOwnerRepo(c.in)
		if ok != c.ok || o != c.owner || r != c.repo {
			t.Errorf("splitOwnerRepo(%q): got (%q, %q, %v), want (%q, %q, %v)",
				c.in, o, r, ok, c.owner, c.repo, c.ok)
		}
	}
}
