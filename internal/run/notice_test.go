package run

import (
	"bytes"
	"strings"
	"testing"
)

func TestWriteInjectionNotice_TTYHasAnsi(t *testing.T) {
	var buf bytes.Buffer
	writeInjectionNotice(&buf, 4, true)
	got := buf.String()
	if !strings.Contains(got, "\033[32m") || !strings.Contains(got, "\033[0m") {
		t.Fatalf("TTY branch must include ANSI green + reset; got %q", got)
	}
	if !strings.Contains(got, "jitenv: injected 4 variables") {
		t.Fatalf("missing message body: %q", got)
	}
	if !strings.HasSuffix(got, "\n") {
		t.Fatalf("notice should end with newline: %q", got)
	}
}

func TestWriteInjectionNotice_NoTTYIsPlain(t *testing.T) {
	var buf bytes.Buffer
	writeInjectionNotice(&buf, 4, false)
	got := buf.String()
	if strings.Contains(got, "\033[") {
		t.Fatalf("non-TTY branch must not emit ANSI escapes; got %q", got)
	}
	if got != "jitenv: injected 4 variables\n" {
		t.Fatalf("plain output mismatch: %q", got)
	}
}

func TestWriteInjectionNotice_SingularPlural(t *testing.T) {
	var buf bytes.Buffer
	writeInjectionNotice(&buf, 1, false)
	if got := buf.String(); got != "jitenv: injected 1 variable\n" {
		t.Fatalf("singular form: %q", got)
	}
	buf.Reset()
	writeInjectionNotice(&buf, 2, false)
	if got := buf.String(); got != "jitenv: injected 2 variables\n" {
		t.Fatalf("plural form for 2: %q", got)
	}
	buf.Reset()
	writeInjectionNotice(&buf, 0, false)
	// The function itself does not gate on N==0; the caller skips the
	// call when there's nothing to report. We still document the
	// formatting here so a regression in the gate is visible.
	if got := buf.String(); got != "jitenv: injected 0 variables\n" {
		t.Fatalf("zero form: %q", got)
	}
}
