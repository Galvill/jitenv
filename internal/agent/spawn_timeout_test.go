package agent

import (
	"testing"
	"time"
)

// TestSpawnTimeout exercises the JITENV_AGENT_SPAWN_TIMEOUT knob added in
// issue #264: a valid duration overrides the default, while an empty,
// unparseable, zero, or negative value falls back to the 10s default.
func TestSpawnTimeout(t *testing.T) {
	// An empty value is treated identically to "unset" by the parser, so
	// it stands in for the unset case (t.Setenv can only set, not unset).
	cases := []struct {
		name string
		env  string
		want time.Duration
	}{
		{name: "empty (unset)", env: "", want: defaultSpawnTimeout},
		{name: "valid seconds", env: "20s", want: 20 * time.Second},
		{name: "valid sub-second", env: "1500ms", want: 1500 * time.Millisecond},
		{name: "unparseable", env: "soon", want: defaultSpawnTimeout},
		{name: "bare number rejected", env: "5", want: defaultSpawnTimeout},
		{name: "zero falls back", env: "0s", want: defaultSpawnTimeout},
		{name: "negative falls back", env: "-3s", want: defaultSpawnTimeout},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("JITENV_AGENT_SPAWN_TIMEOUT", tc.env)
			if got := spawnTimeout(); got != tc.want {
				t.Fatalf("spawnTimeout() = %s, want %s", got, tc.want)
			}
		})
	}
}

// TestSpawnTimeoutDefault is a focused check that the default matches the
// 10s countdown ceiling the inline-unlock flow relies on (#264).
func TestSpawnTimeoutDefault(t *testing.T) {
	if defaultSpawnTimeout != 10*time.Second {
		t.Fatalf("defaultSpawnTimeout = %s, want 10s", defaultSpawnTimeout)
	}
}
