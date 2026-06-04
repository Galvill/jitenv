package discover

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

// writeFiles creates a temp dir populated with the given (empty) files
// and returns its path.
func writeFiles(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o600); err != nil {
			t.Fatalf("write %s: %v", n, err)
		}
	}
	return dir
}

// scanCommands returns just the suggested command names for folder.
func scanCommands(t *testing.T, dir string) []string {
	t.Helper()
	return Commands(dir)
}

// sortedEqual compares two string slices ignoring order.
func sortedEqual(a, b []string) bool {
	a = append([]string(nil), a...)
	b = append([]string(nil), b...)
	sort.Strings(a)
	sort.Strings(b)
	return reflect.DeepEqual(a, b)
}

func TestScan_PerEcosystem(t *testing.T) {
	cases := []struct {
		name  string
		files []string
		want  []string
	}{
		{"npm", []string{"package.json"}, []string{"npm", "node", "npx"}},
		{"dockerfile", []string{"Dockerfile"}, []string{"docker"}},
		{"compose-yml", []string{"docker-compose.yml"}, []string{"docker", "docker-compose"}},
		{"compose-yaml", []string{"compose.yaml"}, []string{"docker", "docker-compose"}},
		{"cargo", []string{"Cargo.toml"}, []string{"cargo", "rustc"}},
		{"go", []string{"go.mod"}, []string{"go"}},
		{"pyproject", []string{"pyproject.toml"}, []string{"python", "python3", "pip"}},
		{"requirements", []string{"requirements.txt"}, []string{"python", "python3", "pip"}},
		{"pipfile", []string{"Pipfile"}, []string{"python", "python3", "pip"}},
		{"gemfile", []string{"Gemfile"}, []string{"bundle", "ruby", "rake"}},
		{"makefile-upper", []string{"Makefile"}, []string{"make"}},
		{"flake", []string{"flake.nix"}, []string{"nix"}},
		{"default-nix", []string{"default.nix"}, []string{"nix"}},
		{"kustomize", []string{"kustomization.yaml"}, []string{"kubectl", "helm"}},
		{"helm-chart", []string{"Chart.yaml"}, []string{"kubectl", "helm"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeFiles(t, tc.files...)
			got := scanCommands(t, dir)
			if !sortedEqual(got, tc.want) {
				t.Errorf("Scan(%v) = %v, want %v", tc.files, got, tc.want)
			}
		})
	}
}

func TestScan_TerraformGlob(t *testing.T) {
	dir := writeFiles(t, "main.tf", "variables.tf", "README.md")
	got := scanCommands(t, dir)
	if !sortedEqual(got, []string{"terraform", "tofu"}) {
		t.Fatalf("got %v, want [terraform tofu]", got)
	}

	// A folder with no .tf files should not suggest terraform.
	dir2 := writeFiles(t, "README.md")
	if got := scanCommands(t, dir2); len(got) != 0 {
		t.Fatalf("expected no suggestions, got %v", got)
	}
}

func TestScan_LockfileSwap_Pnpm(t *testing.T) {
	dir := writeFiles(t, "package.json", "pnpm-lock.yaml")
	got := scanCommands(t, dir)
	for _, c := range got {
		if c == "npm" {
			t.Fatalf("npm should be swapped for pnpm: %v", got)
		}
	}
	if !contains(got, "pnpm") {
		t.Fatalf("expected pnpm in %v", got)
	}
	// node/npx remain alongside pnpm.
	if !sortedEqual(got, []string{"pnpm", "node", "npx"}) {
		t.Fatalf("got %v, want [pnpm node npx]", got)
	}
}

func TestScan_LockfileSwap_YarnAndBun(t *testing.T) {
	yarn := scanCommands(t, writeFiles(t, "package.json", "yarn.lock"))
	if !sortedEqual(yarn, []string{"yarn", "node", "npx"}) {
		t.Errorf("yarn case: got %v", yarn)
	}
	bun := scanCommands(t, writeFiles(t, "package.json", "bun.lockb"))
	if !sortedEqual(bun, []string{"bun", "node", "npx"}) {
		t.Errorf("bun case: got %v", bun)
	}
}

func TestScan_LockfileSwap_NoBaseNoSuggestion(t *testing.T) {
	// pnpm-lock.yaml without package.json yields nothing — the swap
	// only rewrites an existing npm suggestion.
	dir := writeFiles(t, "pnpm-lock.yaml")
	if got := scanCommands(t, dir); len(got) != 0 {
		t.Fatalf("expected no suggestions, got %v", got)
	}
}

func TestScan_PoetryAdds(t *testing.T) {
	dir := writeFiles(t, "pyproject.toml", "poetry.lock")
	got := scanCommands(t, dir)
	if !sortedEqual(got, []string{"python", "python3", "pip", "poetry"}) {
		t.Fatalf("got %v, want python tools + poetry", got)
	}

	// poetry.lock alone (no python marker) adds nothing.
	if got := scanCommands(t, writeFiles(t, "poetry.lock")); len(got) != 0 {
		t.Fatalf("poetry.lock alone should suggest nothing, got %v", got)
	}
}

// TestScan_CaseSensitivity verifies both Makefile and makefile resolve
// to make, exercising the explicit-case-variant handling that keeps
// case-sensitive Linux working.
func TestScan_CaseSensitivity(t *testing.T) {
	upper := scanCommands(t, writeFiles(t, "Makefile"))
	if !sortedEqual(upper, []string{"make"}) {
		t.Errorf("Makefile: got %v", upper)
	}
	lowerD := scanCommands(t, writeFiles(t, "makefile"))
	if !sortedEqual(lowerD, []string{"make"}) {
		t.Errorf("makefile: got %v", lowerD)
	}
}

// TestScan_UnionDedup checks that a folder with multiple markers returns
// the de-duplicated union (docker appears once even though two markers
// emit it).
func TestScan_UnionDedup(t *testing.T) {
	dir := writeFiles(t, "package.json", "Dockerfile", "docker-compose.yml")
	got := scanCommands(t, dir)
	want := []string{"npm", "node", "npx", "docker", "docker-compose"}
	if !sortedEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	// docker must appear exactly once.
	count := 0
	for _, c := range got {
		if c == "docker" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("docker appeared %d times, want 1: %v", count, got)
	}
}

func TestScan_OrderIsRegistryOrder(t *testing.T) {
	dir := writeFiles(t, "Dockerfile", "package.json")
	got := scanCommands(t, dir)
	// package.json is registered before Dockerfile, so its commands lead.
	want := []string{"npm", "node", "npx", "docker"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func TestScan_MissingFolder(t *testing.T) {
	if got := Scan(filepath.Join(t.TempDir(), "does-not-exist")); got != nil {
		t.Fatalf("missing folder should yield nil, got %v", got)
	}
}

func TestScan_ReasonPopulated(t *testing.T) {
	dir := writeFiles(t, "go.mod")
	sugs := Scan(dir)
	if len(sugs) != 1 || sugs[0].Command != "go" || sugs[0].Reason != "go.mod" {
		t.Fatalf("unexpected suggestions: %#v", sugs)
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
