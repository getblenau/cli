package cmd

import "testing"

func TestIsNewer(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"v0.4.1", "0.4.0", true},
		{"v0.4.0", "0.4.0", false},
		{"v0.3.9", "0.4.0", false},
		{"v1.0.0", "0.9.9", true},
		{"v0.4.10", "0.4.9", true}, // numeric, not lexical
		{"v0.4.1-rc1", "0.4.0", true},
		{"garbage", "0.4.0", false},
	}
	for _, c := range cases {
		if got := isNewer(c.latest, c.current); got != c.want {
			t.Errorf("isNewer(%q,%q)=%v want %v", c.latest, c.current, got, c.want)
		}
	}
}
