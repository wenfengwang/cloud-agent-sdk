package testenv

import (
	"errors"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Redacted replaces every scrubbed credential.
const Redacted = "REDACTED"

// Path placeholders of recorded fixtures.
const (
	PlaceholderHome           = "@HOME@"
	PlaceholderHarnessHome    = "@HARNESS_HOME@"
	PlaceholderWorkDir        = "@WORKDIR@"
	PlaceholderWorkDirEncoded = "@WORKDIR_ENCODED@"
)

var (
	bearerPattern = regexp.MustCompile(`Bearer\s+[A-Za-z0-9._~+/=-]+`)
	authPattern   = regexp.MustCompile(`("(?i:authorization)"\s*:\s*")[^"]*(")`)
	apiKeyPattern = regexp.MustCompile(`sk-[A-Za-z0-9_-]{20,}`)
)

// Scrubber removes credentials from recorded output and replaces absolute
// paths with placeholders.
type Scrubber struct {
	secrets []string
	paths   []pathRule
}

type pathRule struct{ value, placeholder string }

// NewScrubber returns a Scrubber for the given credential values.
func NewScrubber(secrets []string) *Scrubber {
	s := &Scrubber{}
	for _, v := range secrets {
		if v != "" {
			s.secrets = append(s.secrets, v)
		}
	}
	// Longest first, so that a value containing another is replaced whole.
	sort.Slice(s.secrets, func(i, j int) bool { return len(s.secrets[i]) > len(s.secrets[j]) })
	return s
}

// AddPath replaces value, and its symlink-resolved form, with placeholder.
func (s *Scrubber) AddPath(placeholder, value string) {
	if value == "" {
		return
	}
	s.paths = append(s.paths, pathRule{value, placeholder})
	if resolved, err := filepath.EvalSymlinks(value); err == nil && resolved != value {
		s.paths = append(s.paths, pathRule{resolved, placeholder})
	}
	sort.SliceStable(s.paths, func(i, j int) bool { return len(s.paths[i].value) > len(s.paths[j].value) })
}

// Scrub replaces credentials, bearer tokens, authorization headers, and API
// keys with Redacted, then paths with their placeholders.
func (s *Scrubber) Scrub(data []byte) []byte {
	out := string(data)
	for _, v := range s.secrets {
		out = strings.ReplaceAll(out, v, Redacted)
	}
	out = bearerPattern.ReplaceAllString(out, "Bearer "+Redacted)
	out = authPattern.ReplaceAllString(out, "${1}"+Redacted+"${2}")
	out = apiKeyPattern.ReplaceAllString(out, Redacted)
	return []byte(s.ScrubPath(out))
}

// ScrubPath replaces only paths with their placeholders. Use it for file
// names, which are never rewritten otherwise.
func (s *Scrubber) ScrubPath(text string) string {
	for _, p := range s.paths {
		text = strings.ReplaceAll(text, p.value, p.placeholder)
	}
	return text
}

// Check fails if data still holds a loaded credential, an API key, or a
// bearer token. The error never repeats the value.
func (s *Scrubber) Check(data []byte) error {
	text := string(data)
	for _, v := range s.secrets {
		if strings.Contains(text, v) {
			return errors.New("testenv: a loaded credential value is still present")
		}
	}
	if apiKeyPattern.MatchString(text) {
		return errors.New("testenv: an API key is still present")
	}
	for _, m := range bearerPattern.FindAllString(text, -1) {
		if m != "Bearer "+Redacted {
			return errors.New("testenv: a bearer token is still present")
		}
	}
	return nil
}

// Substitute replaces placeholders with values.
func Substitute(text string, values map[string]string) string {
	for k, v := range values {
		text = strings.ReplaceAll(text, k, v)
	}
	return text
}
