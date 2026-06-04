package config

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestLiteralPrefix(t *testing.T) {
	cases := map[string]string{
		"/usr/local/bin/*":      "/usr/local/bin/",
		"/usr/local/bin/kube-*": "/usr/local/bin/kube-",
		"/usr/{local,opt}/bin":  "/usr/",
		"/opt/foo":              "/opt/foo", // no wildcard → whole pattern
		"/a/[xy]/z":             "/a/",
		"/a/b?c":                "/a/b",
		`/a/b\*c/*`:             "/a/b*c/", // escaped star is literal
		"*foo":                  "",        // opens with a wildcard
		"/**/x":                 "/",
	}
	for in, want := range cases {
		if got := literalPrefix(in); got != want {
			t.Errorf("literalPrefix(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIndexAnchors(t *testing.T) {
	exactA, _ := filepath.Abs("/usr/local/bin/terraform")
	exactB, _ := filepath.Abs("/opt/homebrew/bin/aws")

	idx := NewIndex([]Mapping{
		{Path: "/usr/local/bin/terraform"},
		{Path: "/opt/homebrew/bin/aws"},
		{Glob: "/usr/local/bin/kubectl-*"},
		{Glob: "/srv/*/bin/*"},
		{CwdGlob: "~/work/**", Commands: []string{"npm"}}, // excluded from anchors
	})

	exact, prefixes := idx.Anchors()

	wantExact := []string{exactA, exactB}
	// Anchors sorts; sort our expectation the same way via the function's
	// contract (sorted ascending).
	if exactA > exactB {
		wantExact = []string{exactB, exactA}
	}
	if !reflect.DeepEqual(exact, wantExact) {
		t.Errorf("exact = %v, want %v", exact, wantExact)
	}

	wantPrefixes := []string{"/srv/", "/usr/local/bin/kubectl-"}
	if !reflect.DeepEqual(prefixes, wantPrefixes) {
		t.Errorf("prefixes = %v, want %v", prefixes, wantPrefixes)
	}
}

func TestIndexAnchorsEmpty(t *testing.T) {
	idx := NewIndex(nil)
	exact, prefixes := idx.Anchors()
	if len(exact) != 0 || len(prefixes) != 0 {
		t.Errorf("empty config should yield no anchors; got exact=%v prefixes=%v", exact, prefixes)
	}
	// cwd_glob-only must also yield zero anchors (served by PATH wrappers).
	idx = NewIndex([]Mapping{{CwdGlob: "~/work", Commands: []string{"npm"}}})
	exact, prefixes = idx.Anchors()
	if len(exact) != 0 || len(prefixes) != 0 {
		t.Errorf("cwd_glob-only should yield no anchors; got exact=%v prefixes=%v", exact, prefixes)
	}
}
