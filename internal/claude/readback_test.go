package claude

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wenfengwang/cloud-agent-sdk/internal/fakeharness"
	"github.com/wenfengwang/cloud-agent-sdk/internal/harness"
	"github.com/wenfengwang/cloud-agent-sdk/internal/message"
	"github.com/wenfengwang/cloud-agent-sdk/internal/testenv"
)

const testSessionID = "0199e1c2-0000-7000-8000-00000000c1a0"

// paths are the temporary paths a fixture is materialized into.
type paths struct {
	home, harnessHome, work string
}

func newPaths(t *testing.T) paths {
	t.Helper()
	return paths{home: t.TempDir(), harnessHome: t.TempDir(), work: t.TempDir()}
}

// values maps the fixture placeholders to p.
func (p paths) values(sessionID string) map[string]string {
	return map[string]string{
		testenv.PlaceholderHome:           p.home,
		testenv.PlaceholderHarnessHome:    p.harnessHome,
		testenv.PlaceholderWorkDir:        p.work,
		testenv.PlaceholderWorkDirEncoded: EncodeWorkDir(resolve(p.work)),
		placeholderSessionID:              sessionID,
	}
}

// scrubber maps p and sessionID back to the fixture placeholders.
func (p paths) scrubber(sessionID string) *testenv.Scrubber {
	s := testenv.NewScrubber(nil)
	s.AddPath(testenv.PlaceholderHome, p.home)
	s.AddPath(testenv.PlaceholderHarnessHome, p.harnessHome)
	s.AddPath(testenv.PlaceholderWorkDir, p.work)
	s.AddPath(testenv.PlaceholderWorkDirEncoded, EncodeWorkDir(resolve(p.work)))
	s.AddPath(placeholderSessionID, sessionID)
	return s
}

// TestReadBack materializes each fixture's harness home and checks that
// reading every turn back yields the golden messages, except for the fields
// the transcript does not persist as streamed (see readBackView).
func TestReadBack(t *testing.T) {
	for _, s := range testenv.Scenarios() {
		t.Run(s.Name, func(t *testing.T) {
			dir := filepath.Join("testdata", s.Name)
			rec := readFixture(t, dir)
			p := newPaths(t)
			testenv.CopyHome(t, dir, p.harnessHome, p.values(testSessionID))
			h := New(harness.Config{WorkDir: p.work, Home: p.harnessHome})
			var got []message.Message
			for i, traffic := range rec.Traffic {
				ref := harness.Ref{SessionID: testSessionID, Data: refData(testSessionID, replayedUUID(t, traffic))}
				msgs, err := h.ReadTurn(context.Background(), ref)
				if err != nil {
					t.Fatalf("turn %d: %v", i+1, err)
				}
				got = append(got, msgs...)
			}
			for _, m := range got {
				requireWellFormed(t, m)
			}
			want := goldenMessages(t, dir)
			n := len(got)
			got = withoutUnstreamed(t, got, want)
			// Design 001 lists the subagent's final assistant message as the
			// one live and read-back difference.
			if extra := n - len(got); (s.Name == "subagent") != (extra > 0) {
				t.Errorf("read back %d messages that were not streamed", extra)
			}
			compareJSON(t, readBackView(t, want), p.scrubber(testSessionID).Scrub(readBackView(t, got)))
		})
	}
}

func TestReadTurnMissingTranscript(t *testing.T) {
	h := New(harness.Config{WorkDir: t.TempDir(), Home: t.TempDir()})
	_, err := h.ReadTurn(context.Background(), harness.Ref{SessionID: testSessionID, Data: refData(testSessionID, "0199e1c2-0000-7000-8000-000000000001")})
	if err == nil {
		t.Fatal("ReadTurn of a missing transcript succeeded")
	}
}

func TestReadTurnUnknownMessage(t *testing.T) {
	dir := filepath.Join("testdata", "text")
	p := newPaths(t)
	testenv.CopyHome(t, dir, p.harnessHome, p.values(testSessionID))
	h := New(harness.Config{WorkDir: p.work, Home: p.harnessHome})
	_, err := h.ReadTurn(context.Background(), harness.Ref{SessionID: testSessionID, Data: refData(testSessionID, "0199e1c2-0000-7000-8000-000000000001")})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("ReadTurn of an unknown user message: %v", err)
	}
}

// replayedUUID returns the uuid of the user message the harness echoed in
// traffic: the turn's user_message_uuid.
func replayedUUID(t *testing.T, traffic []fakeharness.TrafficLine) string {
	t.Helper()
	for _, l := range traffic {
		var e struct {
			Type     string `json:"type"`
			UUID     string `json:"uuid"`
			IsReplay bool   `json:"isReplay"`
		}
		if l.Dir == fakeharness.DirRecv && json.Unmarshal(l.Line, &e) == nil && e.Type == "user" && e.IsReplay {
			return e.UUID
		}
	}
	t.Fatal("traffic has no replayed user message")
	return ""
}

func refData(sessionID, userUUID string) json.RawMessage {
	data, _ := json.Marshal(turnRef{SessionID: sessionID, UserMessageUUID: userUUID})
	return data
}

func goldenMessages(t *testing.T, dir string) []message.Message {
	t.Helper()
	rec := readFixture(t, dir)
	var all []message.Message
	for _, traffic := range rec.Traffic {
		all = append(all, translateTraffic(t, traffic)...)
	}
	return all
}

// withoutUnstreamed drops from back the messages the live stream does not
// carry. The only such messages come from a subagent's final assistant
// message, its text and any thinking: Claude Code persists that message in
// the subagent transcript but streams only its text, as the subagent's tool
// result.
func withoutUnstreamed(t *testing.T, back, live []message.Message) []message.Message {
	t.Helper()
	streamed := map[string]bool{}
	for _, m := range live {
		streamed[m.ID] = true
	}
	var out []message.Message
	for _, m := range back {
		if !streamed[m.ID] && (m.Kind == message.KindAssistant || m.Kind == message.KindReasoning) && m.ParentToolUseID != "" {
			continue
		}
		out = append(out, m)
	}
	return out
}

// readBackView encodes msgs without the fields a read-back does not
// reproduce: Raw is the transcript entry instead of the stream line.
func readBackView(t *testing.T, msgs []message.Message) []byte {
	t.Helper()
	out := make([]message.Message, len(msgs))
	for i, m := range msgs {
		m.Raw = nil
		out[i] = m
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func compareJSON(t *testing.T, want, got []byte) {
	t.Helper()
	if string(want) != string(got) {
		path := filepath.Join(t.TempDir(), "got.json")
		os.WriteFile(path, got, 0o644)
		t.Fatalf("messages differ\ngot (%s):\n%s\nwant:\n%s", path, got, want)
	}
}
