package gitauth

import (
	"strings"
	"testing"
)

func TestParseCloneURL_Accepts(t *testing.T) {
	cases := []struct {
		in          string
		wantCleaned string
		wantBag     string
	}{
		{
			in:          "https://github.com/Galvill/jitenv",
			wantCleaned: "https://github.com/Galvill/jitenv",
			wantBag:     "galvill-jitenv",
		},
		{
			in:          "https://github.com/Galvill/jitenv.git",
			wantCleaned: "https://github.com/Galvill/jitenv.git",
			wantBag:     "galvill-jitenv",
		},
		{
			in:          "https://gitlab.example.com/team/sub/repo",
			wantCleaned: "https://gitlab.example.com/team/sub/repo",
			wantBag:     "sub-repo",
		},
		{
			in:          "https://gitea.acme.test/x/y/",
			wantCleaned: "https://gitea.acme.test/x/y",
			wantBag:     "x-y",
		},
		{
			in:          "  https://github.com/ACME/Awesome.Repo  ",
			wantCleaned: "https://github.com/ACME/Awesome.Repo",
			wantBag:     "acme-awesome-repo",
		},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got, bag, err := ParseCloneURL(c.in)
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if got != c.wantCleaned {
				t.Errorf("cleaned: got %q want %q", got, c.wantCleaned)
			}
			if bag != c.wantBag {
				t.Errorf("bag: got %q want %q", bag, c.wantBag)
			}
		})
	}
}

func TestParseCloneURL_Rejects(t *testing.T) {
	cases := []struct {
		in       string
		errMatch string
	}{
		{"", "empty"},
		{"git@github.com:Galvill/jitenv.git", "ssh-style"},
		{"ssh://git@github.com/Galvill/jitenv.git", "Phase 2"},
		{"http://insecure.test/x/y", "cleartext"},
		{"file:///etc/passwd", "unsupported URL scheme"},
		{"https://oauth2:secret@github.com/x/y", "credential"},
		{"https://", "host"},
		{"https://github.com/", "repository path"},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			_, _, err := ParseCloneURL(c.in)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", c.errMatch)
			}
			if !strings.Contains(err.Error(), c.errMatch) {
				t.Errorf("error %q doesn't contain %q", err.Error(), c.errMatch)
			}
		})
	}
}

func TestDedupeBagName(t *testing.T) {
	cases := []struct {
		hint  string
		taken []string
		want  string
	}{
		{"foo", nil, "foo"},
		{"foo", []string{"foo"}, "foo-2"},
		{"foo", []string{"foo", "foo-2"}, "foo-3"},
		{"foo", []string{"bar"}, "foo"},
	}
	for _, c := range cases {
		t.Run(c.hint, func(t *testing.T) {
			set := map[string]struct{}{}
			for _, n := range c.taken {
				set[n] = struct{}{}
			}
			if got := DedupeBagName(c.hint, set); got != c.want {
				t.Errorf("got %q want %q", got, c.want)
			}
		})
	}
}

func TestSanitizeBagName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Galvill-jitenv", "galvill-jitenv"},
		{"team!/sub@repo", "team-sub-repo"},
		{"   ", "repo"},
		{"...", "repo"},
		{"my.repo.v2", "my-repo-v2"},
	}
	for _, c := range cases {
		if got := sanitizeBagName(c.in); got != c.want {
			t.Errorf("sanitize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
