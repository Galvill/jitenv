package tui

import (
	"testing"

	"github.com/gv/jitenv/internal/config"
)

// TestExpandedVarCount is the regression for #165: the mappings list
// rendered `len(mp.Vars)` which counts *references*, not env vars. A
// VarRef with empty Name expands to N env vars (every key in the bag),
// so a single expand-all ref to a 10-key bag was shown as "1 var"
// instead of "10 vars". Local-source expand-alls are counted exactly;
// remote-source expand-alls return known=false so the renderer shows
// a lower-bound `N+`.
func TestExpandedVarCount(t *testing.T) {
	cfg := &config.Config{
		Sources: map[string]config.SourceConfig{
			"localsrc":  {Type: "local"},
			"remotesrc": {Type: "vault"},
		},
		Secrets: map[string]map[string]string{
			"big":   {"A": "1", "B": "2", "C": "3"},
			"empty": {},
		},
	}
	cases := []struct {
		name      string
		mp        config.Mapping
		wantN     int
		wantKnown bool
	}{
		{
			name:      "named-only counts each ref",
			mp:        config.Mapping{Vars: []config.VarRef{{Name: "X", Source: "localsrc"}, {Name: "Y", Source: "localsrc"}}},
			wantN:     2,
			wantKnown: true,
		},
		{
			name:      "local expand-all expands by bag size",
			mp:        config.Mapping{Vars: []config.VarRef{{Source: "localsrc", Ref: "big"}}},
			wantN:     3,
			wantKnown: true,
		},
		{
			name:      "mixed: named + local expand-all",
			mp:        config.Mapping{Vars: []config.VarRef{{Name: "X", Source: "localsrc"}, {Source: "localsrc", Ref: "big"}}},
			wantN:     4,
			wantKnown: true,
		},
		{
			name:      "remote expand-all returns known=false",
			mp:        config.Mapping{Vars: []config.VarRef{{Source: "remotesrc", Ref: "anybag"}}},
			wantN:     0,
			wantKnown: false,
		},
		{
			name:      "remote expand-all + named still counts named",
			mp:        config.Mapping{Vars: []config.VarRef{{Name: "X", Source: "localsrc"}, {Source: "remotesrc", Ref: "b"}}},
			wantN:     1,
			wantKnown: false,
		},
		{
			name:      "empty bag expand-all counts zero",
			mp:        config.Mapping{Vars: []config.VarRef{{Source: "localsrc", Ref: "empty"}}},
			wantN:     0,
			wantKnown: true,
		},
		{
			name:      "expand-all naming a missing bag counts zero",
			mp:        config.Mapping{Vars: []config.VarRef{{Source: "localsrc", Ref: "nope"}}},
			wantN:     0,
			wantKnown: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n, known := expandedVarCount(cfg, &tc.mp)
			if n != tc.wantN || known != tc.wantKnown {
				t.Errorf("got (n=%d, known=%v); want (n=%d, known=%v)", n, known, tc.wantN, tc.wantKnown)
			}
		})
	}
}
