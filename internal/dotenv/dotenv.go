// Package dotenv parses dotenv-style KEY=VALUE blocks into pairs. It is
// the single shared parser used by both the TUI bulk-import screen and
// the `jitenv bag import` CLI command (#70, #250) so the two entry
// points accept exactly the same syntax.
package dotenv

import (
	"fmt"
	"strings"
)

// Pair is one KEY = VALUE pair parsed from a dotenv-style block.
type Pair struct {
	Key   string
	Value string
	Line  int // 1-based source line for error reporting
}

// ParseError is a single parse error tied to a source line. It exists
// as its own type so callers can render line numbers cleanly.
type ParseError struct {
	Line int
	Msg  string
}

func (e ParseError) Error() string {
	return fmt.Sprintf("line %d: %s", e.Line, e.Msg)
}

// Parse parses dotenv-style content into pairs. Recognised syntax:
//
//	KEY=VALUE                   bare value, no surrounding whitespace required
//	KEY="quoted value"          double-quoted; supports \n \r \t \\ \" escapes
//	KEY='single quoted'         single-quoted; no escape processing
//	export KEY=VALUE            optional shell-style export prefix
//	# comment                   ignored
//	(blank line)                ignored
//
// Lines without an '=' separator (after stripping the optional 'export'
// prefix) are reported as parse errors. The caller is expected to
// surface the errors to the user and refuse to import on any error.
//
// On a duplicate key inside the same input the later value wins; the
// caller decides what to do with collisions against the existing bag.
func Parse(input string) ([]Pair, []ParseError) {
	var pairs []Pair
	var errs []ParseError

	// Normalise line endings so a Windows-style \r\n paste behaves like
	// a Unix one. We split on \n then strip a trailing \r per line.
	lines := strings.Split(input, "\n")
	for i, raw := range lines {
		lineNo := i + 1
		line := strings.TrimRight(raw, "\r")
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Strip an optional "export " prefix.
		if rest, ok := stripExport(line); ok {
			line = rest
		}

		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			errs = append(errs, ParseError{Line: lineNo, Msg: "no '=' separator"})
			continue
		}

		key := strings.TrimSpace(line[:eq])
		if key == "" {
			errs = append(errs, ParseError{Line: lineNo, Msg: "empty key"})
			continue
		}
		if !isValidEnvKey(key) {
			errs = append(errs, ParseError{Line: lineNo, Msg: fmt.Sprintf("invalid key %q", key)})
			continue
		}

		rawVal := strings.TrimLeft(line[eq+1:], " \t")
		val, err := unquoteValue(rawVal)
		if err != nil {
			errs = append(errs, ParseError{Line: lineNo, Msg: err.Error()})
			continue
		}
		pairs = append(pairs, Pair{Key: key, Value: val, Line: lineNo})
	}
	return pairs, errs
}

// stripExport returns (rest, true) if line begins with the shell
// "export " prefix. The check requires at least one trailing whitespace
// character so we don't accidentally strip a key that happens to start
// with "export" (e.g. "exported=1").
func stripExport(line string) (string, bool) {
	const p = "export"
	if !strings.HasPrefix(line, p) {
		return line, false
	}
	if len(line) == len(p) {
		return line, false
	}
	c := line[len(p)]
	if c != ' ' && c != '\t' {
		return line, false
	}
	return strings.TrimLeft(line[len(p):], " \t"), true
}

// unquoteValue strips surrounding quotes (single or double) and, for
// double quotes only, processes a small set of backslash escapes. A
// trailing inline comment (` # ...`) is dropped from bare values; this
// matches the behaviour most dotenv readers settle on.
func unquoteValue(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	switch s[0] {
	case '"':
		// Find the matching closing quote, honouring backslash escapes.
		var b strings.Builder
		i := 1
		for i < len(s) {
			c := s[i]
			if c == '\\' && i+1 < len(s) {
				switch s[i+1] {
				case 'n':
					b.WriteByte('\n')
				case 'r':
					b.WriteByte('\r')
				case 't':
					b.WriteByte('\t')
				case '\\':
					b.WriteByte('\\')
				case '"':
					b.WriteByte('"')
				default:
					b.WriteByte(s[i+1])
				}
				i += 2
				continue
			}
			if c == '"' {
				// Closing quote. Anything after is ignored (typically
				// whitespace or an inline comment).
				return b.String(), nil
			}
			b.WriteByte(c)
			i++
		}
		return "", fmt.Errorf("unterminated double quote")
	case '\'':
		end := strings.IndexByte(s[1:], '\'')
		if end < 0 {
			return "", fmt.Errorf("unterminated single quote")
		}
		return s[1 : 1+end], nil
	default:
		// Bare value: strip an inline ` # comment` then trim trailing
		// whitespace.
		if idx := strings.Index(s, " #"); idx >= 0 {
			s = s[:idx]
		}
		return strings.TrimRight(s, " \t"), nil
	}
}

// isValidEnvKey enforces the conservative POSIX-ish rule used elsewhere
// in this codebase: identifiers must start with a letter or underscore
// and contain only letters, digits or underscores.
func isValidEnvKey(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r == '_':
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case i > 0 && r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return true
}
