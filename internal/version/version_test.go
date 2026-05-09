package version

import "testing"

func TestFormat(t *testing.T) {
	cases := []struct {
		name              string
		ver, commit, date string
		want              string
	}{
		{
			name:   "all fields populated",
			ver:    "0.5.0",
			commit: "abc1234",
			date:   "2026-05-06T12:34:56Z",
			want:   "jitenv 0.5.0 (commit abc1234, built 2026-05-06T12:34:56Z)",
		},
		{
			name:   "dev build with empty commit and date",
			ver:    "dev",
			commit: "",
			date:   "",
			want:   "jitenv dev",
		},
		{
			name:   "commit but no date",
			ver:    "0.5.0",
			commit: "abc1234",
			date:   "",
			want:   "jitenv 0.5.0 (commit abc1234)",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Format(tc.ver, tc.commit, tc.date)
			if got != tc.want {
				t.Errorf("Format(%q, %q, %q) = %q, want %q",
					tc.ver, tc.commit, tc.date, got, tc.want)
			}
		})
	}
}

func TestShort_FollowsSpec(t *testing.T) {
	// The acceptance criterion fixes the `jitenv -v` output to a single
	// line of the form `jitenv <version>` — guard that here so a future
	// edit to Format() doesn't drift the flag's output.
	prev := Version
	t.Cleanup(func() { Version = prev })
	Version = "1.2.3"
	if got, want := Short(), "jitenv 1.2.3"; got != want {
		t.Errorf("Short() = %q, want %q", got, want)
	}
}
