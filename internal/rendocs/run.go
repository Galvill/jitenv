package rendocs

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Options configures a Run.
type Options struct {
	// DocsDir is the directory whose files are scanned + rewritten.
	DocsDir string
	// Tag is the release tag to substitute. If empty, ResolveTag is
	// used to discover it from the environment / gh.
	Tag string
	// DryRun, when true, computes the changes and logs them but writes
	// nothing to disk.
	DryRun bool
	// Out / Err receive human-readable progress + the diff summary.
	Out io.Writer
	Err io.Writer
}

// Result reports what a Run did.
type Result struct {
	Tag     string
	Changed []string // relative paths rewritten (or that would be, in DryRun)
}

// Run resolves the tag, walks DocsDir, rewrites the marked spans in
// every file, and returns the set of files that changed. It is a no-op
// (empty Result.Changed) when nothing needs updating, which the
// workflow uses to avoid empty commits.
func Run(ctx context.Context, o Options) (*Result, error) {
	if o.Out == nil {
		o.Out = io.Discard
	}
	if o.Err == nil {
		o.Err = io.Discard
	}

	tag := strings.TrimSpace(o.Tag)
	if tag == "" {
		var err error
		tag, err = ResolveTag(ctx)
		if err != nil {
			return nil, err
		}
	}
	if !IsStableTag(tag) {
		return nil, fmt.Errorf("rendocs: tag %q is a prerelease/RC; website-update is stable-only", tag)
	}

	rel := Release{Tag: tag}
	res := &Result{Tag: tag}

	err := filepath.WalkDir(o.DocsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".md" && ext != ".html" {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rendered, changed, err := RenderFile(path, string(raw), rel)
		if err != nil {
			return err
		}
		if !changed {
			return nil
		}
		relPath, _ := filepath.Rel(o.DocsDir, path)
		res.Changed = append(res.Changed, relPath)
		fmt.Fprintf(o.Out, "render: %s\n", relPath)
		if !o.DryRun {
			// Preserve the file's existing mode.
			info, statErr := os.Stat(path)
			mode := os.FileMode(0o644)
			if statErr == nil {
				mode = info.Mode().Perm()
			}
			if werr := os.WriteFile(path, []byte(rendered), mode); werr != nil {
				return werr
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	if len(res.Changed) == 0 {
		fmt.Fprintln(o.Out, "render: no changes (already at "+tag+")")
	}
	return res, nil
}

// ResolveTag discovers the release tag, preferring the workflow's
// GITHUB_REF / GITHUB_REF_NAME, then falling back to the gh CLI's view
// of the latest release. Used when Options.Tag is empty.
func ResolveTag(ctx context.Context) (string, error) {
	if name := os.Getenv("GITHUB_REF_NAME"); name != "" && looksLikeTag(name) {
		return name, nil
	}
	if ref := os.Getenv("GITHUB_REF"); ref != "" {
		if t := strings.TrimPrefix(ref, "refs/tags/"); t != ref {
			return t, nil
		}
	}
	// Fall back to `gh release view --json tagName`.
	tag, err := ghLatestTag(ctx)
	if err != nil {
		return "", fmt.Errorf("rendocs: no tag in GITHUB_REF/GITHUB_REF_NAME and gh fallback failed: %w", err)
	}
	return tag, nil
}

func looksLikeTag(s string) bool {
	return strings.HasPrefix(s, "v") && !strings.ContainsAny(s, "/ ")
}

// ghLatestTag shells out to gh to read the latest release's tag.
// Indirected through a var so tests can stub it.
var ghLatestTag = func(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, "gh", "release", "view", "--json", "tagName", "--jq", ".tagName")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("gh release view: %w (%s)", err, strings.TrimSpace(stderr.String()))
	}
	tag := strings.TrimSpace(stdout.String())
	if tag == "" {
		return "", fmt.Errorf("gh release view returned an empty tagName")
	}
	return tag, nil
}
