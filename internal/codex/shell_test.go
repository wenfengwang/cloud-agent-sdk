package codex

import "testing"

func TestUnwrapShell(t *testing.T) {
	cases := []struct{ in, want string }{
		{`/bin/zsh -lc 'cat notes.txt'`, `cat notes.txt`},
		{`/bin/bash -lc 'echo cloudagent-shell-check'`, `echo cloudagent-shell-check`},
		{`bash -c 'ls src/*.go'`, `ls src/*.go`},
		{`/bin/zsh -lc 'printf '"'"'%s\n'"'"' hi'`, `printf '%s\n' hi`},
		{`/bin/bash -lc 'echo it'\''s'`, `echo it's`},
		{`/bin/zsh -lc "echo \"quoted\" \$HOME"`, `echo "quoted" $HOME`},
		{`/bin/zsh -lc ls`, `ls`},
		{`ls -la`, `ls -la`},
		{`/bin/zsh -lc 'a' 'b'`, `/bin/zsh -lc 'a' 'b'`},
		{`/bin/zsh -lc 'unterminated`, `/bin/zsh -lc 'unterminated`},
	}
	for _, c := range cases {
		if got := unwrapShell(c.in); got != c.want {
			t.Errorf("unwrapShell(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestJoinArgv(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{[]string{"/bin/zsh", "-lc", "cat notes.txt"}, "cat notes.txt"},
		{[]string{"bash", "-c", "ls"}, "ls"},
		{[]string{"/bin/zsh", "-lc", "printf '%s\\n' \"it's\""}, `printf '%s\n' "it's"`},
		{[]string{"ls", "-la"}, "ls -la"},
		{[]string{"echo", "two words", "it's"}, `echo 'two words' 'it'"'"'s'`},
		{[]string{"echo", ""}, `echo ''`},
	}
	for _, c := range cases {
		if got := unwrapShell(joinArgv(c.in)); got != c.want {
			t.Errorf("unwrapShell(joinArgv(%q)) = %q, want %q", c.in, got, c.want)
		}
	}
}
