package tui

import (
	"strings"
	"testing"

	"github.com/gv/jitenv/internal/version"
)

// TestRenderFooter_IncludesVersionRightAligned guards the global footer
// contract: the copyright text stays visible centred-ish, and the
// version sits to its right when the terminal is wide enough. Reviewer
// ergonomics — when a screenshot lands in a bug report, the version
// should be readable without needing to dump the binary.
func TestRenderFooter_IncludesVersionRightAligned(t *testing.T) {
	prev := version.Version
	t.Cleanup(func() { version.Version = prev })
	version.Version = "9.9.9"

	out := renderFooter(120)
	if !strings.Contains(out, "jitenv 9.9.9") {
		t.Fatalf("footer missing version: %q", out)
	}
	if !strings.Contains(out, "jitenv — MIT") {
		t.Fatalf("footer missing copyright: %q", out)
	}
	copyrightIdx := strings.Index(out, "jitenv — MIT")
	versionIdx := strings.LastIndex(out, "jitenv 9.9.9")
	if versionIdx <= copyrightIdx {
		t.Fatalf("version should sit to the right of copyright: %q", out)
	}
}

// TestRenderFooter_DropsVersionWhenNarrow falls back to the centred
// copyright on tight widths so the footer never wraps or clobbers the
// status line above it. The version is always reachable via -v.
func TestRenderFooter_DropsVersionWhenNarrow(t *testing.T) {
	prev := version.Version
	t.Cleanup(func() { version.Version = prev })
	version.Version = "9.9.9"

	out := renderFooter(20)
	if strings.Contains(out, "9.9.9") {
		t.Fatalf("narrow footer should drop version, got %q", out)
	}
}
