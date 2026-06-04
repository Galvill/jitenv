package config

import (
	"strings"
	"testing"
)

func TestWarnings(t *testing.T) {
	tests := []struct {
		name      string
		mappings  []Mapping
		wantCount int
		// wantSubstrs, if set, must all appear in the joined String()
		// output of the warnings.
		wantSubstrs []string
		// wantDuplicate asserts the Duplicate flag of the single
		// expected warning (only checked when wantCount == 1).
		wantDuplicate bool
	}{
		{
			name: "same-name different-source warns",
			mappings: []Mapping{{
				Path: "/bin/x",
				Vars: []VarRef{
					{Name: "DATABASE_URL", Source: "vault", Ref: "r1"},
					{Name: "OTHER", Source: "vault", Ref: "r2"},
					{Name: "Z", Source: "vault", Ref: "r3"},
					{Name: "DATABASE_URL", Source: "aws", Ref: "r4"},
				},
			}},
			wantCount: 1,
			wantSubstrs: []string{
				`env var "DATABASE_URL" is set twice`,
				`vars[0] from source "vault"`,
				`vars[3] from source "aws"`,
				`vars[3] wins at fetch time`,
			},
		},
		{
			name: "same-name same-source-ref-key is a duplicate",
			mappings: []Mapping{{
				Path: "/bin/x",
				Vars: []VarRef{
					{Name: "TOKEN", Source: "vault", Ref: "r1", Key: "k"},
					{Name: "TOKEN", Source: "vault", Ref: "r1", Key: "k"},
				},
			}},
			wantCount:     1,
			wantDuplicate: true,
			wantSubstrs: []string{
				`redundant duplicate`,
			},
		},
		{
			name: "same-name same-source different-ref warns (not duplicate)",
			mappings: []Mapping{{
				Path: "/bin/x",
				Vars: []VarRef{
					{Name: "TOKEN", Source: "vault", Ref: "r1"},
					{Name: "TOKEN", Source: "vault", Ref: "r2"},
				},
			}},
			wantCount:     1,
			wantDuplicate: false,
		},
		{
			name: "different names do not warn",
			mappings: []Mapping{{
				Path: "/bin/x",
				Vars: []VarRef{
					{Name: "A", Source: "vault", Ref: "r1"},
					{Name: "B", Source: "aws", Ref: "r2"},
				},
			}},
			wantCount: 0,
		},
		{
			name: "empty name (expand-whole-bag) is skipped",
			mappings: []Mapping{{
				Path: "/bin/x",
				Vars: []VarRef{
					{Name: "", Source: "vault", Ref: "r1"},
					{Name: "", Source: "aws", Ref: "r2"},
				},
			}},
			wantCount: 0,
		},
		{
			name: "literal value vs sourced same name warns",
			mappings: []Mapping{{
				Path: "/bin/x",
				Vars: []VarRef{
					{Name: "GIT_ASKPASS", Value: "/path/to/shim"},
					{Name: "GIT_ASKPASS", Source: "vault", Ref: "r1"},
				},
			}},
			wantCount: 1,
			wantSubstrs: []string{
				`vars[0] from literal value`,
				`vars[1] from source "vault"`,
			},
		},
		{
			name: "two identical literal values are a duplicate",
			mappings: []Mapping{{
				Path: "/bin/x",
				Vars: []VarRef{
					{Name: "X", Value: "same"},
					{Name: "X", Value: "same"},
				},
			}},
			wantCount:     1,
			wantDuplicate: true,
		},
		{
			name: "collision counts are per-mapping, not cross-mapping",
			mappings: []Mapping{
				{
					Path: "/bin/a",
					Vars: []VarRef{{Name: "X", Source: "vault", Ref: "r1"}},
				},
				{
					Path: "/bin/b",
					Vars: []VarRef{{Name: "X", Source: "aws", Ref: "r2"}},
				},
			},
			wantCount: 0,
		},
		{
			name: "triple collision yields two warnings against first slot",
			mappings: []Mapping{{
				Path: "/bin/x",
				Vars: []VarRef{
					{Name: "X", Source: "a", Ref: "r1"},
					{Name: "X", Source: "b", Ref: "r2"},
					{Name: "X", Source: "c", Ref: "r3"},
				},
			}},
			wantCount: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Config{Version: Version, Mappings: tt.mappings}
			got := c.Warnings()
			if len(got) != tt.wantCount {
				t.Fatalf("Warnings() returned %d warnings, want %d: %v", len(got), tt.wantCount, got)
			}
			if tt.wantCount == 1 && got[0].Duplicate != tt.wantDuplicate {
				t.Errorf("Duplicate = %v, want %v", got[0].Duplicate, tt.wantDuplicate)
			}
			var joined strings.Builder
			for _, w := range got {
				joined.WriteString(w.String())
				joined.WriteByte('\n')
			}
			for _, sub := range tt.wantSubstrs {
				if !strings.Contains(joined.String(), sub) {
					t.Errorf("warning output missing %q\ngot:\n%s", sub, joined.String())
				}
			}
		})
	}
}

func TestWarningsTripleCollisionTargetsFirstSlot(t *testing.T) {
	c := &Config{Version: Version, Mappings: []Mapping{{
		Path: "/bin/x",
		Vars: []VarRef{
			{Name: "X", Source: "a", Ref: "r1"},
			{Name: "X", Source: "b", Ref: "r2"},
			{Name: "X", Source: "c", Ref: "r3"},
		},
	}}}
	got := c.Warnings()
	if len(got) != 2 {
		t.Fatalf("want 2 warnings, got %d", len(got))
	}
	for i, w := range got {
		if w.VarIdxA != 0 {
			t.Errorf("warning %d: VarIdxA = %d, want 0 (first declaration)", i, w.VarIdxA)
		}
	}
	if got[0].VarIdxB != 1 || got[1].VarIdxB != 2 {
		t.Errorf("VarIdxB sequence = (%d,%d), want (1,2)", got[0].VarIdxB, got[1].VarIdxB)
	}
}
