package rendocs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fixtureTag = "v0.14.0"

// TestGolden renders each .input fixture and asserts it matches the
// matching .golden file byte-for-byte. This is the load-bearing test:
// it pins both the version substitution AND the "narrative untouched"
// guarantee (the fixtures embed prose mentioning the old version that
// must survive verbatim).
func TestGolden(t *testing.T) {
	cases := []struct {
		input  string
		golden string
		ext    string
	}{
		{"quickstart.input.md", "quickstart.golden.md", ".md"},
		{"index.input.html", "index.golden.html", ".html"},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			in := readFixture(t, tc.input)
			want := readFixture(t, tc.golden)
			got, err := Render(in, Release{Tag: fixtureTag}, tc.ext)
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			if got != want {
				t.Errorf("rendered output mismatch for %s\n--- got ---\n%s\n--- want ---\n%s",
					tc.input, got, want)
			}
		})
	}
}

// TestNarrativeUntouched is an explicit, focused assertion that prose
// mentioning the old version is preserved (the automation must only
// touch marked spans).
func TestNarrativeUntouched(t *testing.T) {
	in := readFixture(t, "quickstart.input.md")
	got, err := Render(in, Release{Tag: fixtureTag}, ".md")
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	// Normalize CRLF so the assertion holds regardless of how the
	// fixture was checked out (a Windows checkout would be CRLF absent
	// the testdata .gitattributes; this keeps the test robust either way).
	gotLF := strings.ReplaceAll(got, "\r\n", "\n")
	if !strings.Contains(gotLF, "It talks\nabout v0.13.0 in prose") {
		t.Errorf("narrative prose mentioning the old version was modified; got:\n%s", got)
	}
}

func TestRenderIdempotent(t *testing.T) {
	in := readFixture(t, "quickstart.input.md")
	once, err := Render(in, Release{Tag: fixtureTag}, ".md")
	if err != nil {
		t.Fatal(err)
	}
	twice, err := Render(once, Release{Tag: fixtureTag}, ".md")
	if err != nil {
		t.Fatal(err)
	}
	if once != twice {
		t.Error("Render is not idempotent")
	}
}

func TestRenderFileNoChange(t *testing.T) {
	golden := readFixture(t, "quickstart.golden.md")
	_, changed, err := RenderFile("quickstart.md", golden, Release{Tag: fixtureTag})
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Error("rendering an already-current file reported a change")
	}
}

func TestBare(t *testing.T) {
	if got := (Release{Tag: "v0.14.0"}).Bare(); got != "0.14.0" {
		t.Errorf("Bare() = %q, want 0.14.0", got)
	}
	if got := (Release{Tag: "0.14.0"}).Bare(); got != "0.14.0" {
		t.Errorf("Bare() on already-bare = %q", got)
	}
}

func TestIsStableTag(t *testing.T) {
	stable := []string{"v0.14.0", "0.14.0", "v1.0.0"}
	pre := []string{"v0.14.0-rc.1", "v0.14.0-beta", "v1.0.0-alpha.2", ""}
	for _, s := range stable {
		if !IsStableTag(s) {
			t.Errorf("IsStableTag(%q) = false, want true", s)
		}
	}
	for _, s := range pre {
		if IsStableTag(s) {
			t.Errorf("IsStableTag(%q) = true, want false", s)
		}
	}
}

// TestChangedFraction pins the metric that drives the safety bail:
// small intra-line edits stay small; whole-line / structural mangling
// balloons.
func TestChangedFraction(t *testing.T) {
	// Identical → 0.
	if f := changedFraction("abc\ndef\n", "abc\ndef\n"); f != 0 {
		t.Errorf("identical changedFraction = %v, want 0", f)
	}
	// One short inner edit inside a long line → small fraction.
	long := strings.Repeat("x", 100) + "v0.13.0" + strings.Repeat("y", 100) + "\n"
	long2 := strings.Repeat("x", 100) + "v0.14.0" + strings.Repeat("y", 100) + "\n"
	if f := changedFraction(long, long2); f > 0.5 {
		t.Errorf("tiny intra-line edit changedFraction = %v, want <=0.5", f)
	}
	// Whole file rewritten → ~1.0 (> 0.5, trips the bail).
	if f := changedFraction("aaaa\nbbbb\ncccc\n", "zzzz\nwwww\nqqqq\n"); f <= 0.5 {
		t.Errorf("full rewrite changedFraction = %v, want >0.5", f)
	}
}

// TestSafetyBailWiring asserts the >50% guard is actually wired into
// RenderFile by driving it down the bail branch: a tiny file whose marked
// span holds a long original inner string is rewritten to the short tag,
// so the rewritten bytes dominate the file and changedFraction > 0.5.
func TestSafetyBailWiring(t *testing.T) {
	// Legit edit: must NOT bail.
	in := readFixture(t, "quickstart.input.md")
	if _, _, err := RenderFile("quickstart.md", in, Release{Tag: fixtureTag}); err != nil {
		t.Fatalf("legit render unexpectedly bailed: %v", err)
	}

	// Bail path: a marked span whose original inner text is long relative
	// to the whole file. Replacing it with the short tag rewrites the
	// majority of the file's bytes, so the guard must fire. This is what a
	// marker-handling bug looks like in practice.
	inner := strings.Repeat("Z", 100)
	content := "<!-- VERSION:start -->" + inner + "<!-- VERSION:end -->\n"
	rendered, changed, err := RenderFile("docs/x.md", content, Release{Tag: fixtureTag})
	if err == nil {
		t.Fatalf("expected RenderFile to bail with TooLargeError, got changed=%v rendered=%q", changed, rendered)
	}
	var tle *TooLargeError
	if !errors.As(err, &tle) {
		t.Fatalf("expected *TooLargeError, got %T: %v", err, err)
	}
	if tle.ChangedFrac <= 0.5 {
		t.Errorf("TooLargeError.ChangedFrac = %v, want > 0.5", tle.ChangedFrac)
	}
	// On bail RenderFile must NOT return the mutated content.
	if rendered != "" || changed {
		t.Errorf("bail returned content; rendered=%q changed=%v", rendered, changed)
	}
	// And the error type formats sensibly.
	if !strings.Contains(tle.Error(), "docs/x.md") || !strings.Contains(tle.Error(), "%") {
		t.Errorf("TooLargeError.Error() = %q", tle.Error())
	}
}

// TestRenderMultilineSpan verifies the (?s) flag: a version string that a
// human has moved onto its own line (markers and content split across
// newlines) is still updated.
func TestRenderMultilineSpan(t *testing.T) {
	content := "intro\n<!-- VERSION:start -->\nv0.13.0\n<!-- VERSION:end -->\noutro\n"
	got, err := Render(content, Release{Tag: fixtureTag}, ".md")
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	want := "intro\n<!-- VERSION:start -->v0.14.0<!-- VERSION:end -->\noutro\n"
	if got != want {
		t.Errorf("multiline span not updated:\n got: %q\nwant: %q", got, want)
	}
	if strings.Contains(got, "v0.13.0") {
		t.Errorf("old version survived in multiline span: %q", got)
	}
}

// TestRenderUnbalancedMarkers verifies a start marker with no matching
// end (a mangled edit) fails loudly instead of silently no-op'ing.
func TestRenderUnbalancedMarkers(t *testing.T) {
	content := "intro\n<!-- VERSION:start -->v0.13.0\noutro\n" // no VERSION:end
	if _, err := Render(content, Release{Tag: fixtureTag}, ".md"); err == nil {
		t.Fatal("expected Render to reject unbalanced markers, got nil")
	}
	// An end with no start is equally malformed.
	content2 := "intro\nv0.13.0<!-- VERSION:end -->\noutro\n"
	if _, err := Render(content2, Release{Tag: fixtureTag}, ".md"); err == nil {
		t.Fatal("expected Render to reject a lone end marker, got nil")
	}
}

// TestRunWritesAndIsNoOp exercises the file-walking Run end to end on a
// temp copy of the docs fixtures, including the no-op second pass.
func TestRunWritesAndIsNoOp(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "quickstart.md"), readFixture(t, "quickstart.input.md"))
	writeFile(t, filepath.Join(dir, "index.html"), readFixture(t, "index.input.html"))
	// A file with no markers must be left untouched and not reported.
	writeFile(t, filepath.Join(dir, "concepts.md"), "# Concepts\n\nNo markers here.\n")

	res, err := Run(context.Background(), Options{DocsDir: dir, Tag: fixtureTag})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Changed) != 2 {
		t.Fatalf("expected 2 changed files, got %d: %v", len(res.Changed), res.Changed)
	}
	if got := readFile(t, filepath.Join(dir, "quickstart.md")); got != readFixture(t, "quickstart.golden.md") {
		t.Errorf("quickstart.md not rendered to golden:\n%s", got)
	}

	// Second pass: nothing should change.
	res2, err := Run(context.Background(), Options{DocsDir: dir, Tag: fixtureTag})
	if err != nil {
		t.Fatalf("Run (2nd): %v", err)
	}
	if len(res2.Changed) != 0 {
		t.Errorf("expected no changes on second pass, got %v", res2.Changed)
	}
}

func TestRunRejectsRC(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "quickstart.md"), readFixture(t, "quickstart.input.md"))
	_, err := Run(context.Background(), Options{DocsDir: dir, Tag: "v0.14.0-rc.1"})
	if err == nil {
		t.Fatal("expected Run to reject an RC tag")
	}
	if !strings.Contains(err.Error(), "stable-only") {
		t.Errorf("error did not mention stable-only: %v", err)
	}
}

func TestResolveTagFromEnv(t *testing.T) {
	t.Setenv("GITHUB_REF_NAME", "v0.14.0")
	t.Setenv("GITHUB_REF", "refs/tags/v0.14.0")
	tag, err := ResolveTag(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if tag != "v0.14.0" {
		t.Errorf("ResolveTag = %q, want v0.14.0", tag)
	}
}

func TestResolveTagFromRef(t *testing.T) {
	t.Setenv("GITHUB_REF_NAME", "")
	t.Setenv("GITHUB_REF", "refs/tags/v0.15.1")
	tag, err := ResolveTag(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if tag != "v0.15.1" {
		t.Errorf("ResolveTag = %q, want v0.15.1", tag)
	}
}

// --- helpers ---

func readFixture(t *testing.T, name string) string {
	t.Helper()
	return readFile(t, filepath.Join("testdata", name))
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
