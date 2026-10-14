package codex

import (
	"path"
	"strings"
)

// Codex runs model commands through a login shell and reports them as one
// string ("/bin/zsh -lc '<cmd>'") in the app-server stream and as an argv
// in the rollout. Design 001 reports the command the model asked for, so
// the rollout argv is joined into the stream's form and then unwrapped to
// <cmd>.

var shells = map[string]bool{"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true, "fish": true}

func isShellWrapper(argv []string) bool {
	return len(argv) == 3 && shells[path.Base(argv[0])] && (argv[1] == "-lc" || argv[1] == "-c")
}

// unwrapShell removes a login-shell wrapper from a command string. A string
// that is not exactly "<shell> -c|-lc <one word>" is returned unchanged.
func unwrapShell(cmd string) string {
	argv, ok := splitWords(cmd)
	if !ok || !isShellWrapper(argv) {
		return cmd
	}
	return argv[2]
}

// joinArgv joins an argv into one command string with POSIX shell quoting,
// the form the app-server reports, so that unwrapShell treats both alike.
func joinArgv(argv []string) string {
	quoted := make([]string, len(argv))
	for i, a := range argv {
		quoted[i] = quoteWord(a)
	}
	return strings.Join(quoted, " ")
}

func quoteWord(s string) string {
	if s != "" && strings.IndexFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_./=:,+@%", r))
	}) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

// splitWords splits s into words by POSIX shell quoting rules (single
// quotes, double quotes, backslashes). It reports false for unterminated
// quotes.
func splitWords(s string) ([]string, bool) {
	var words []string
	var cur strings.Builder
	inWord := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n':
			if inWord {
				words = append(words, cur.String())
				cur.Reset()
				inWord = false
			}
		case c == '\'':
			end := strings.IndexByte(s[i+1:], '\'')
			if end < 0 {
				return nil, false
			}
			cur.WriteString(s[i+1 : i+1+end])
			i += end + 1
			inWord = true
		case c == '"':
			i++
			for ; i < len(s) && s[i] != '"'; i++ {
				if s[i] == '\\' && i+1 < len(s) && strings.IndexByte("$`\"\\\n", s[i+1]) >= 0 {
					i++
				}
				cur.WriteByte(s[i])
			}
			if i >= len(s) {
				return nil, false
			}
			inWord = true
		case c == '\\':
			if i+1 >= len(s) {
				return nil, false
			}
			i++
			cur.WriteByte(s[i])
			inWord = true
		default:
			cur.WriteByte(c)
			inWord = true
		}
	}
	if inWord {
		words = append(words, cur.String())
	}
	return words, true
}
