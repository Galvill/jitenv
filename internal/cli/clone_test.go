package cli

import (
	"strings"
	"testing"

	"github.com/gv/jitenv/internal/config"
	"github.com/gv/jitenv/internal/gitauth"
)

// TestCloneGeneratedMapping_Validates is a regression: the mapping
// shape `jitenv clone` produces must pass config.Validate. If the
// validator ever grows a new rule that rejects this shape (literal-
// value VarRef + cwd_glob + commands=[git]), this test fails loud
// before users do.
func TestCloneGeneratedMapping_Validates(t *testing.T) {
	cfg := &config.Config{
		Version: config.Version,
		Sources: map[string]config.SourceConfig{
			"local": {Type: "local"},
		},
		Secrets: map[string]map[string]string{
			"galvill-jitenv": {"token": "enc:v2:..."}, // pretend envelope
		},
		Mappings: []config.Mapping{
			{
				CwdGlob:  "/home/user/galvill-jitenv/**",
				Commands: []string{"git"},
				Vars: []config.VarRef{
					{Name: gitauth.JitenvGitTokenEnv, Source: "local", Ref: "galvill-jitenv", Key: "token"},
					{Name: "GIT_ASKPASS", Value: "/home/user/.local/share/jitenv/bin/git-askpass.sh"},
				},
			},
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("generated clone config must validate: %v", err)
	}
}

func TestLiteralValueVarRef_RejectsMixed(t *testing.T) {
	cfg := &config.Config{
		Version: config.Version,
		Sources: map[string]config.SourceConfig{"local": {Type: "local"}},
		Mappings: []config.Mapping{
			{
				CwdGlob:  "/x/**",
				Commands: []string{"git"},
				Vars: []config.VarRef{
					// value + source set simultaneously is ambiguous — must error.
					{Name: "FOO", Value: "literal", Source: "local", Ref: "bag", Key: "k"},
				},
			},
		},
		Secrets: map[string]map[string]string{"bag": {"k": "v"}},
	}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "exclusive") {
		t.Fatalf("expected exclusivity error, got %v", err)
	}
}

func TestLiteralValueVarRef_RequiresName(t *testing.T) {
	cfg := &config.Config{
		Version:  config.Version,
		Sources:  map[string]config.SourceConfig{"local": {Type: "local"}},
		Mappings: []config.Mapping{{CwdGlob: "/x/**", Commands: []string{"git"}, Vars: []config.VarRef{{Value: "lit"}}}},
	}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "name is required") {
		t.Fatalf("expected name-required error, got %v", err)
	}
}

func TestStripEnv(t *testing.T) {
	in := []string{"A=1", "JITENV_GIT_TOKEN=stale", "B=2", "JITENV_GIT_TOKEN=stale2"}
	out := stripEnv(in, "JITENV_GIT_TOKEN")
	if len(out) != 2 || out[0] != "A=1" || out[1] != "B=2" {
		t.Errorf("stripEnv = %v, want [A=1 B=2]", out)
	}
}

func TestDefaultDestDir(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://github.com/Galvill/jitenv", "jitenv"},
		{"https://github.com/Galvill/jitenv.git", "jitenv"},
		{"https://gitea.test/team/repo.git", "repo"},
	}
	for _, c := range cases {
		if got := defaultDestDir(c.in); got != c.want {
			t.Errorf("defaultDestDir(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestTrimNewline(t *testing.T) {
	cases := []struct{ in, want string }{
		{"foo\n", "foo"},
		{"foo\r\n", "foo"},
		{"foo", "foo"},
		{"\n\n", ""},
	}
	for _, c := range cases {
		if got := string(trimNewline([]byte(c.in))); got != c.want {
			t.Errorf("trimNewline(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
