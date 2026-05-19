package tui

import (
	"testing"

	"github.com/gv/jitenv/internal/config"
)

// TestMappingFormScreen_isPartial is the regression for #163: an Esc
// on a half-filled mapping should prompt Resume/Discard rather than
// silently save inert cruft. isPartial drives the decision.
func TestMappingFormScreen_isPartial(t *testing.T) {
	cases := []struct {
		name string
		mp   config.Mapping
		want bool
	}{
		{
			name: "fully empty",
			mp:   config.Mapping{},
			want: false, // isEmpty() catches this — isPartial only fires between empty and complete
		},
		{
			name: "complete path mapping",
			mp: config.Mapping{
				Path: "/usr/local/bin/foo",
				Vars: []config.VarRef{{Name: "X", Source: "s"}},
			},
			want: false,
		},
		{
			name: "complete cwd mapping",
			mp: config.Mapping{
				CwdGlob:  "/work/**",
				Commands: []string{"npm"},
				Vars:     []config.VarRef{{Name: "X", Source: "s"}},
			},
			want: false,
		},
		{
			name: "target without vars",
			mp:   config.Mapping{Path: "/x"},
			want: true,
		},
		{
			name: "glob without vars",
			mp:   config.Mapping{Glob: "/x/**"},
			want: true,
		},
		{
			name: "cwd_glob without commands or vars",
			mp:   config.Mapping{CwdGlob: "/work/**"},
			want: true,
		},
		{
			name: "cwd_glob + commands but no vars",
			mp: config.Mapping{
				CwdGlob:  "/work/**",
				Commands: []string{"npm"},
			},
			want: true,
		},
		{
			name: "cwd_glob + vars but no commands",
			mp: config.Mapping{
				CwdGlob: "/work/**",
				Vars:    []config.VarRef{{Name: "X", Source: "s"}},
			},
			want: true,
		},
		{
			name: "vars without target",
			mp: config.Mapping{
				Vars: []config.VarRef{{Name: "X", Source: "s"}},
			},
			want: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &rootModel{cfg: &config.Config{
				Mappings: []config.Mapping{tc.mp},
			}}
			s := &mappingFormScreen{root: r, idx: 0}
			if got := s.isPartial(); got != tc.want {
				t.Errorf("isPartial = %v, want %v", got, tc.want)
			}
		})
	}
}
