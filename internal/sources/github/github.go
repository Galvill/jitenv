// Package github implements a Source that reads GitHub Variables
// (repo / org / environment scope) using a personal access token.
//
// IMPORTANT: GitHub Actions, Codespaces, and Dependabot SECRETS cannot
// be read back via the API — only their metadata. This source therefore
// supports VARIABLES only, and returns an explicit error if a secret
// reference is attempted.
package github

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/go-github/v66/github"
	"golang.org/x/oauth2"

	"github.com/gv/jitenv/internal/sources"
	"github.com/gv/jitenv/pkg/source"
)

const TypeName = "github"

func init() {
	sources.Register(TypeName, New)
	sources.RegisterSchema(TypeName, schema())
}

func schema() []source.ParamField {
	return []source.ParamField{
		{
			Key: "token", Label: "Personal access token", Required: true, Sensitive: true,
			Help: "Classic or fine-grained PAT with read access to repo/org variables. " +
				"Encrypted at rest under the master key.",
		},
		{
			Key: "api_url", Label: "API URL",
			Help: "Default https://api.github.com. For GHES use https://your.host/api/v3/. " +
				"NOTE: GitHub does not expose Actions secret VALUES via the API — only names. " +
				"Map secret values from a different source (local bag / AWS) by matching the name.",
		},
	}
}

// New constructs a GitHub source. Recognized cfg keys:
//
//	token   string  PAT with scopes for the variables you want to read
//	api_url string  optional, for GitHub Enterprise
func New(cfg map[string]any) (source.Source, error) {
	token, _ := cfg["token"].(string)
	if token == "" {
		return nil, errors.New("github: token is required")
	}
	apiURL, _ := cfg["api_url"].(string)
	return &githubSource{token: token, apiURL: apiURL}, nil
}

type githubSource struct {
	token  string
	apiURL string
}

func (g *githubSource) Name() string { return TypeName }

func (g *githubSource) Schema() []source.ParamField { return schema() }

func (g *githubSource) client(ctx context.Context) (*github.Client, error) {
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: g.token})
	httpClient := oauth2.NewClient(ctx, ts)
	if g.apiURL != "" && g.apiURL != "https://api.github.com" {
		c, err := github.NewClient(httpClient).WithEnterpriseURLs(g.apiURL, g.apiURL)
		if err != nil {
			return nil, err
		}
		return c, nil
	}
	return github.NewClient(httpClient), nil
}

func (g *githubSource) Validate(ctx context.Context) error {
	c, err := g.client(ctx)
	if err != nil {
		return err
	}
	_, resp, err := c.Users.Get(ctx, "")
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("github: GET /user returned %s", resp.Status)
	}
	return nil
}

// Fetch reads a variable.
//
//	ref.ID    "owner/repo" (repo scope), or "org" (org scope)
//	ref.Key   variable name (REQUIRED)
//	ref.Extra keys:
//	  scope        one of: repo (default), org, env
//	  environment  required when scope=env
func (g *githubSource) Fetch(ctx context.Context, ref source.SecretRef) (map[string]string, error) {
	if ref.Key == "" {
		return nil, errors.New("github: ref.Key (variable name) is required")
	}
	scope := strings.ToLower(strings.TrimSpace(ref.Extra["scope"]))
	if scope == "" {
		scope = "repo"
	}
	if scope == "secret" || scope == "secrets" {
		return nil, errors.New("github: secret values are not retrievable via API; use Variables instead")
	}
	c, err := g.client(ctx)
	if err != nil {
		return nil, err
	}

	var value string
	var resp *github.Response
	switch scope {
	case "repo":
		owner, repo, ok := splitOwnerRepo(ref.ID)
		if !ok {
			return nil, fmt.Errorf("github: ref.ID must be \"owner/repo\" for scope=repo, got %q", ref.ID)
		}
		v, r, err := c.Actions.GetRepoVariable(ctx, owner, repo, ref.Key)
		if err != nil {
			return nil, fmt.Errorf("github: GetRepoVariable %s/%s/%s: %w", owner, repo, ref.Key, err)
		}
		resp = r
		value = v.Value
	case "org":
		if ref.ID == "" || strings.Contains(ref.ID, "/") {
			return nil, fmt.Errorf("github: ref.ID must be \"org\" for scope=org, got %q", ref.ID)
		}
		v, r, err := c.Actions.GetOrgVariable(ctx, ref.ID, ref.Key)
		if err != nil {
			return nil, fmt.Errorf("github: GetOrgVariable %s/%s: %w", ref.ID, ref.Key, err)
		}
		resp = r
		value = v.Value
	case "env":
		owner, repo, ok := splitOwnerRepo(ref.ID)
		if !ok {
			return nil, fmt.Errorf("github: ref.ID must be \"owner/repo\" for scope=env, got %q", ref.ID)
		}
		envName := ref.Extra["environment"]
		if envName == "" {
			return nil, errors.New("github: scope=env requires extra.environment")
		}
		v, r, err := c.Actions.GetEnvVariable(ctx, owner, repo, envName, ref.Key)
		if err != nil {
			return nil, fmt.Errorf("github: GetEnvVariable %s/%s/%s/%s: %w", owner, repo, envName, ref.Key, err)
		}
		resp = r
		value = v.Value
	default:
		return nil, fmt.Errorf("github: unknown scope %q", scope)
	}

	if resp != nil && resp.StatusCode >= 400 {
		return nil, fmt.Errorf("github: %s returned %s", scope, resp.Status)
	}
	return map[string]string{ref.Key: value}, nil
}

func splitOwnerRepo(s string) (owner, repo string, ok bool) {
	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}
