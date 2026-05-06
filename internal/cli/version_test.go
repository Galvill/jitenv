package cli

import "testing"

func TestFormatVersion(t *testing.T) {
	cases := []struct {
		name              string
		ver, commit, date string
		want              string
	}{
		{
			name:   "all fields populated",
			ver:    "0.1.0",
			commit: "abc1234",
			date:   "2026-05-06T12:34:56Z",
			want:   "jitenv 0.1.0 (commit abc1234, built 2026-05-06T12:34:56Z)",
		},
		{
			name:   "dev build with empty commit and date",
			ver:    "dev",
			commit: "",
			date:   "",
			want:   "jitenv dev",
		},
		{
			name:   "dev build with commit but no date",
			ver:    "dev",
			commit: "abc1234",
			date:   "",
			want:   "jitenv dev (commit abc1234)",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := formatVersion(tc.ver, tc.commit, tc.date)
			if got != tc.want {
				t.Errorf("formatVersion(%q, %q, %q) = %q, want %q",
					tc.ver, tc.commit, tc.date, got, tc.want)
			}
		})
	}
}
