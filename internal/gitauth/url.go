package gitauth

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// ParseCloneURL parses an HTTPS git URL and returns the absolute
// clone URL plus a derived bag-name hint. Only `https://` is
// accepted in Phase 1 of #179; ssh:// and git@host:owner/repo
// shorthand are rejected with a clear error so the user knows the
// limitation up-front rather than after the clone command races
// to ask ssh-agent for a key.
//
// Bag-name derivation: hostname's first label, then the URL path's
// last two non-empty segments (last is stripped of a trailing `.git`),
// joined with `-`, lowercased, with characters outside [a-z0-9-]
// collapsed to `-`. Examples:
//
//	https://github.com/Galvill/jitenv         → "galvill-jitenv"
//	https://github.com/Galvill/jitenv.git     → "galvill-jitenv"
//	https://gitlab.example.com/team/sub/repo  → "team-repo"
//	https://gitea.acme.test/x/y               → "x-y"
//
// The first-label drop ("github.com" → ignored) keeps names
// human-readable; users typically only clone from one or two hosts.
// Collision handling is the caller's job — DedupeBagName below.
func ParseCloneURL(raw string) (cleanedURL string, bagHint string, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", errors.New("clone url is empty")
	}
	// Reject git's ssh shorthand before url.Parse swallows it as
	// an opaque scheme.
	if strings.HasPrefix(raw, "git@") || strings.Contains(raw, "@") && !strings.Contains(raw, "://") {
		return "", "", fmt.Errorf("ssh-style URLs (%q) aren't supported yet; use the https URL — Phase 2 of #179 will add ssh keys", raw)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", fmt.Errorf("parse url: %w", err)
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		// OK.
	case "http":
		return "", "", errors.New("http:// is rejected; jitenv-clone refuses to send a PAT in cleartext")
	case "ssh", "git":
		return "", "", fmt.Errorf("%s:// URLs aren't supported yet — Phase 2 of #179 will add ssh keys", u.Scheme)
	default:
		return "", "", fmt.Errorf("unsupported URL scheme %q (want https)", u.Scheme)
	}
	if u.User != nil {
		return "", "", errors.New("URL embeds a credential (user@host); pass the bare https URL — jitenv prompts for the PAT separately")
	}
	if u.Host == "" {
		return "", "", errors.New("URL is missing a host")
	}
	path := strings.Trim(u.Path, "/")
	if path == "" {
		return "", "", errors.New("URL is missing a repository path")
	}

	// Build the cleaned URL: original scheme + host + path, no
	// userinfo, no fragment, no query (git ignores them but they'd
	// confuse the bag-name derivation later).
	cleaned := (&url.URL{
		Scheme: u.Scheme,
		Host:   u.Host,
		Path:   "/" + path,
	}).String()

	return cleaned, deriveBagName(u.Host, path), nil
}

func deriveBagName(host, path string) string {
	// host: take the first label ("github.com" → "github", "x.y.z" → "x")
	hostLabel := host
	if i := strings.IndexByte(hostLabel, '.'); i >= 0 {
		hostLabel = hostLabel[:i]
	}

	// Drop the host from the bag name — it's usually noise (every
	// repo a user clones is on the same handful of hosts). The owner/
	// repo pair is enough to make the bag identifiable.
	_ = hostLabel

	parts := strings.Split(path, "/")
	// Strip empty segments left by leading/trailing slashes.
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return "repo"
	}
	// Strip `.git` from the last segment so `jitenv.git` → `jitenv`.
	last := strings.TrimSuffix(out[len(out)-1], ".git")
	out[len(out)-1] = last

	// Take the last two segments as the bag name source. For
	// `Galvill/jitenv` that's both; for `team/sub/repo` it's
	// `sub-repo` which is what people remember.
	if len(out) > 2 {
		out = out[len(out)-2:]
	}
	return sanitizeBagName(strings.Join(out, "-"))
}

// sanitizeBagName lowercases and replaces every character outside
// [a-z0-9-] with `-`, collapsing consecutive replacements. Leaves
// a stable identifier safe to use as both a TOML table key and a
// directory-name fragment.
func sanitizeBagName(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	b.Grow(len(s))
	prevDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash && b.Len() > 0 {
				b.WriteRune('-')
				prevDash = true
			}
		}
	}
	// Trim trailing dash.
	out := b.String()
	out = strings.TrimRight(out, "-")
	if out == "" {
		return "repo"
	}
	return out
}

// DedupeBagName picks a free name in the user's existing bag set.
// Returns hint unchanged when free, otherwise hint-2, hint-3, …
// Bag set is the keys of Config.Secrets — caller passes that map.
//
// Used by `jitenv clone` to handle the case where two repos derive
// the same bag-name hint (e.g. cloning both gitea.test/Galvill/jitenv
// and github.com/Galvill/jitenv into the same machine).
func DedupeBagName(hint string, existing map[string]struct{}) string {
	if _, taken := existing[hint]; !taken {
		return hint
	}
	for n := 2; ; n++ {
		candidate := fmt.Sprintf("%s-%d", hint, n)
		if _, taken := existing[candidate]; !taken {
			return candidate
		}
	}
}
