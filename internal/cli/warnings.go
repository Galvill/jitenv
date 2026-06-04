package cli

import (
	"fmt"
	"io"

	"github.com/gv/jitenv/internal/config"
)

// emitConfigWarnings prints one advisory line per collision warning
// (#251) to w. It is the shared post-decrypt surface used by the
// unlock and clone paths so a user who writes/loads a config with an
// intra-mapping env-var collision sees it once at load time rather than
// silently getting the last-wins value at fetch time. cfg MUST already
// be decrypted (var.Name/Source are sealed envelopes otherwise, #235).
// Returns the number of warnings emitted.
func emitConfigWarnings(w io.Writer, cfg *config.Config) int {
	warnings := cfg.Warnings()
	for _, warn := range warnings {
		fmt.Fprintf(w, "warning: %s\n", warn.String())
	}
	return len(warnings)
}

// reportConfigWarnings is the `jitenv config validate` post-decrypt
// surface (#251). It prints each warning to w and, when strict is set
// and any warning exists, returns a non-zero-exit error so CI can gate
// on a clean config. cfg MUST already be decrypted. By default
// (strict=false) it always returns nil — warnings are advisory and the
// command exits 0.
func reportConfigWarnings(w io.Writer, cfg *config.Config, strict bool) error {
	n := emitConfigWarnings(w, cfg)
	if strict && n > 0 {
		return fmt.Errorf("%d warning(s) found (--strict)", n)
	}
	return nil
}
