package testenv

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/wenfengwang/cloud-agent-sdk/core/internal/fakeharness"
)

// testCodec reads lines of the form {"s":"<stream>","t":"<text>"}.
type testCodec struct{}

func (testCodec) Streams(lines []fakeharness.TrafficLine) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		var d struct{ S string }
		if json.Unmarshal(l.Line, &d) == nil {
			out[i] = d.S
		}
	}
	return out
}

func (testCodec) Text(line []byte) string {
	var d struct{ T string }
	_ = json.Unmarshal(line, &d)
	return d.T
}

func (testCodec) SetText(line []byte, text string) ([]byte, error) {
	var d map[string]string
	if err := json.Unmarshal(line, &d); err != nil {
		return nil, err
	}
	d["t"] = text
	return json.Marshal(d)
}

func recv(line string) fakeharness.TrafficLine {
	return fakeharness.TrafficLine{Dir: fakeharness.DirRecv, Line: json.RawMessage(line)}
}

func TestScrubStreams(t *testing.T) {
	s := NewScrubber(nil)
	s.AddPath("@WORKDIR@", "/tmp/work-123")
	lines := []fakeharness.TrafficLine{
		recv(`{"s":"a","t":"See /tmp/wo"}`),
		recv(`{"other":"line"}`),
		recv(`{"s":"a","t":"rk-123/x.txt"}`),
		recv(`{"s":"a","t":" now."}`),
		recv(`{"s":"b","t":"plain "}`),
		recv(`{"s":"b","t":"text"}`),
	}
	got, err := ScrubStreams(lines, testCodec{}, s)
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, l := range got {
		texts = append(texts, string(l.Line))
	}
	want := []string{
		`{"s":"a","t":"See @WORKDIR@/x.txt now."}`,
		`{"other":"line"}`,
		`{"s":"b","t":"plain "}`,
		`{"s":"b","t":"text"}`,
	}
	if strings.Join(texts, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(texts, "\n"), strings.Join(want, "\n"))
	}
}

func TestScrubStreamsRedactsSplitSecret(t *testing.T) {
	s := NewScrubber([]string{"hunter2-secret"})
	lines := []fakeharness.TrafficLine{recv(`{"s":"a","t":"key hunter2"}`), recv(`{"s":"a","t":"-secret"}`)}
	got, err := ScrubStreams(lines, testCodec{}, s)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || strings.Contains(string(got[0].Line), "hunter2") {
		t.Fatalf("got %s", got)
	}
}
