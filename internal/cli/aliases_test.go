package cli

import (
	"testing"

	"github.com/spf13/cobra"
)

// TestCommandAliases pins the short aliases for the two
// most-typed commands (#219) so a future refactor of the cobra
// wiring can't silently drop them.
func TestCommandAliases(t *testing.T) {
	root := &cobra.Command{Use: "jitenv"}
	for _, sub := range subcommands() {
		root.AddCommand(sub)
	}

	for _, tc := range []struct {
		alias string
		want  string
	}{
		{"c", "config"},
		{"u", "unlock"},
	} {
		cmd, _, err := root.Find([]string{tc.alias})
		if err != nil {
			t.Errorf("Find(%q) error: %v", tc.alias, err)
			continue
		}
		if cmd.Name() != tc.want {
			t.Errorf("alias %q resolved to %q, want %q", tc.alias, cmd.Name(), tc.want)
		}
	}
}
