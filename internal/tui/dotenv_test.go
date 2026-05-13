package tui

import (
	"strings"
	"testing"
)

func TestParseDotenv_Simple(t *testing.T) {
	pairs, errs := parseDotenv("FOO=bar\nBAZ=qux\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected errs: %v", errs)
	}
	if len(pairs) != 2 {
		t.Fatalf("want 2 pairs, got %d (%v)", len(pairs), pairs)
	}
	if pairs[0].Key != "FOO" || pairs[0].Value != "bar" {
		t.Errorf("pair0 = %+v", pairs[0])
	}
	if pairs[1].Key != "BAZ" || pairs[1].Value != "qux" {
		t.Errorf("pair1 = %+v", pairs[1])
	}
}

func TestParseDotenv_DoubleQuoted(t *testing.T) {
	pairs, errs := parseDotenv(`FOO="hello world"` + "\n" + `BAR="line1\nline2"` + "\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected errs: %v", errs)
	}
	if pairs[0].Value != "hello world" {
		t.Errorf("FOO value = %q", pairs[0].Value)
	}
	if pairs[1].Value != "line1\nline2" {
		t.Errorf("BAR value = %q (want escape-processed)", pairs[1].Value)
	}
}

func TestParseDotenv_DoubleQuoted_EscapedQuote(t *testing.T) {
	pairs, errs := parseDotenv(`KEY="he said \"hi\""` + "\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected errs: %v", errs)
	}
	if pairs[0].Value != `he said "hi"` {
		t.Errorf("value = %q", pairs[0].Value)
	}
}

func TestParseDotenv_SingleQuoted_NoEscape(t *testing.T) {
	pairs, errs := parseDotenv(`FOO='no \n escape here'` + "\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected errs: %v", errs)
	}
	if pairs[0].Value != `no \n escape here` {
		t.Errorf("value = %q (single-quoted must not process escapes)", pairs[0].Value)
	}
}

func TestParseDotenv_ExportPrefix(t *testing.T) {
	pairs, errs := parseDotenv("export FOO=bar\nexport\tBAZ=qux\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected errs: %v", errs)
	}
	if pairs[0].Key != "FOO" || pairs[0].Value != "bar" {
		t.Errorf("pair0 = %+v", pairs[0])
	}
	if pairs[1].Key != "BAZ" || pairs[1].Value != "qux" {
		t.Errorf("pair1 = %+v", pairs[1])
	}
}

func TestParseDotenv_ExportLookalike(t *testing.T) {
	// A key literally named "exported" is not the export prefix.
	pairs, errs := parseDotenv("exported=1\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected errs: %v", errs)
	}
	if pairs[0].Key != "exported" || pairs[0].Value != "1" {
		t.Errorf("pair0 = %+v", pairs[0])
	}
}

func TestParseDotenv_CommentsAndBlanks(t *testing.T) {
	input := `# a comment
   # indented comment

FOO=bar

# trailing comment
BAZ=qux
`
	pairs, errs := parseDotenv(input)
	if len(errs) != 0 {
		t.Fatalf("unexpected errs: %v", errs)
	}
	if len(pairs) != 2 {
		t.Fatalf("want 2 pairs, got %d", len(pairs))
	}
}

func TestParseDotenv_InlineCommentOnBareValue(t *testing.T) {
	pairs, errs := parseDotenv("FOO=bar # trailing\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected errs: %v", errs)
	}
	if pairs[0].Value != "bar" {
		t.Errorf("value = %q (inline comment should be stripped)", pairs[0].Value)
	}
}

func TestParseDotenv_HashInsideQuotes(t *testing.T) {
	pairs, errs := parseDotenv(`FOO="a # not a comment"` + "\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected errs: %v", errs)
	}
	if pairs[0].Value != "a # not a comment" {
		t.Errorf("value = %q (# inside quotes must be preserved)", pairs[0].Value)
	}
}

func TestParseDotenv_Malformed_NoEquals(t *testing.T) {
	_, errs := parseDotenv("FOO=bar\nthis is not a pair\nBAZ=qux\n")
	if len(errs) != 1 {
		t.Fatalf("want 1 err, got %d (%v)", len(errs), errs)
	}
	if errs[0].Line != 2 {
		t.Errorf("err line = %d, want 2", errs[0].Line)
	}
	if !strings.Contains(errs[0].Msg, "no '='") {
		t.Errorf("err msg = %q", errs[0].Msg)
	}
}

func TestParseDotenv_Malformed_EmptyKey(t *testing.T) {
	_, errs := parseDotenv("=value\n")
	if len(errs) != 1 {
		t.Fatalf("want 1 err, got %d", len(errs))
	}
	if !strings.Contains(errs[0].Msg, "empty key") {
		t.Errorf("err msg = %q", errs[0].Msg)
	}
}

func TestParseDotenv_Malformed_InvalidKey(t *testing.T) {
	_, errs := parseDotenv("9FOO=bar\n")
	if len(errs) != 1 {
		t.Fatalf("want 1 err, got %d", len(errs))
	}
	if !strings.Contains(errs[0].Msg, "invalid key") {
		t.Errorf("err msg = %q", errs[0].Msg)
	}
}

func TestParseDotenv_UnterminatedQuote(t *testing.T) {
	_, errs := parseDotenv(`FOO="oops` + "\n")
	if len(errs) != 1 {
		t.Fatalf("want 1 err, got %d", len(errs))
	}
	if !strings.Contains(errs[0].Msg, "unterminated") {
		t.Errorf("err msg = %q", errs[0].Msg)
	}
}

func TestParseDotenv_Empty(t *testing.T) {
	pairs, errs := parseDotenv("")
	if len(errs) != 0 {
		t.Fatalf("unexpected errs: %v", errs)
	}
	if len(pairs) != 0 {
		t.Fatalf("want 0 pairs, got %d", len(pairs))
	}
}

func TestParseDotenv_WhitespaceOnly(t *testing.T) {
	pairs, errs := parseDotenv("   \n\t\n\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected errs: %v", errs)
	}
	if len(pairs) != 0 {
		t.Fatalf("want 0 pairs, got %d", len(pairs))
	}
}

func TestParseDotenv_CRLFEndings(t *testing.T) {
	pairs, errs := parseDotenv("FOO=bar\r\nBAZ=qux\r\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected errs: %v", errs)
	}
	if len(pairs) != 2 || pairs[0].Value != "bar" || pairs[1].Value != "qux" {
		t.Errorf("pairs = %v", pairs)
	}
}

func TestParseDotenv_TrimsTrailingWhitespaceOnBare(t *testing.T) {
	pairs, errs := parseDotenv("FOO=bar   \n")
	if len(errs) != 0 {
		t.Fatalf("unexpected errs: %v", errs)
	}
	if pairs[0].Value != "bar" {
		t.Errorf("value = %q", pairs[0].Value)
	}
}

func TestParseDotenv_DuplicateKeyLaterWins(t *testing.T) {
	pairs, errs := parseDotenv("FOO=one\nFOO=two\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected errs: %v", errs)
	}
	if len(pairs) != 2 {
		t.Fatalf("want 2 pairs, got %d", len(pairs))
	}
	// Parser keeps both; the caller decides the merge policy.
	if pairs[0].Value != "one" || pairs[1].Value != "two" {
		t.Errorf("pairs = %v", pairs)
	}
}

func TestParseDotenv_EmptyValueAllowed(t *testing.T) {
	pairs, errs := parseDotenv("FOO=\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected errs: %v", errs)
	}
	if pairs[0].Key != "FOO" || pairs[0].Value != "" {
		t.Errorf("pair = %+v", pairs[0])
	}
}
