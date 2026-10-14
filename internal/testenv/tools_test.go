package testenv

import "testing"

func TestMatchVersionOutput(t *testing.T) {
	cases := []struct {
		out, want string
		ok        bool
	}{
		{"2.1.300 (Claude Code)\n", "2.1.300", true},
		{"codex-cli 0.162.1\n", "0.162.1", true},
		{"codex-cli 0.162.10\n", "0.162.1", false},
		{"2.1.30 (Claude Code)", "2.1.300", false},
		{"", "2.1.300", false},
	}
	for _, c := range cases {
		if got := matchVersionOutput(c.out, c.want); got != c.ok {
			t.Errorf("matchVersionOutput(%q, %q) = %v, want %v", c.out, c.want, got, c.ok)
		}
	}
}
