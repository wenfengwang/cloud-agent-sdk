package testenv

import (
	"fmt"

	"github.com/wenfengwang/cloud-agent-sdk/core/internal/fakeharness"
)

// DeltaCodec reads and rewrites the text of a harness's streamed-text delta
// lines. Scrubbing line by line misses a value the harness split across
// deltas, so WriteFixture scrubs each stream's concatenated text through it.
type DeltaCodec interface {
	// Streams returns the delta stream of each line of one process's
	// traffic, in order, or "" for a line that is not a delta. It sees all
	// lines because a delta line may not name its stream by itself.
	Streams(lines []fakeharness.TrafficLine) []string
	// Text returns the text of a delta line.
	Text(line []byte) string
	// SetText returns a delta line with its text replaced.
	SetText(line []byte, text string) ([]byte, error)
}

// ScrubStreams scrubs the concatenated text of every delta stream in lines.
// A stream whose text scrubbing changes is merged into its first delta,
// which then holds the whole scrubbed text; other streams keep their
// deltas, which line scrubbing handles.
func ScrubStreams(lines []fakeharness.TrafficLine, codec DeltaCodec, s *Scrubber) ([]fakeharness.TrafficLine, error) {
	streams := codec.Streams(lines)
	text := map[string]string{}
	for i, l := range lines {
		if streams[i] != "" {
			text[streams[i]] += codec.Text(l.Line)
		}
	}
	out := make([]fakeharness.TrafficLine, 0, len(lines))
	merged := map[string]bool{}
	for i, l := range lines {
		stream := streams[i]
		if stream == "" {
			out = append(out, l)
			continue
		}
		scrubbed := string(s.Scrub([]byte(text[stream])))
		if scrubbed == text[stream] {
			out = append(out, l)
			continue
		}
		if merged[stream] {
			continue
		}
		merged[stream] = true
		line, err := codec.SetText(l.Line, scrubbed)
		if err != nil {
			return nil, fmt.Errorf("testenv: rewrite delta of stream %s: %w", stream, err)
		}
		l.Line = line
		out = append(out, l)
	}
	return out, nil
}
