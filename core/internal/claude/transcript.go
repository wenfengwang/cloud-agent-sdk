package claude

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/wenfengwang/cloud-agent-sdk/core/internal/message"
)

// transcriptLine is one entry of a transcript file with what read-back needs
// to select and order it.
type transcriptLine struct {
	raw       []byte
	parent    string // the subagent call the entry belongs to, if any
	timestamp time.Time
	prompt    bool // the turn's user message
}

// entryHead is the part of a transcript entry that selects and orders it.
type entryHead struct {
	Type      string `json:"type"`
	UUID      string `json:"uuid"`
	PromptID  string `json:"promptId"`
	Timestamp string `json:"timestamp"`
	Message   struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// subagentMeta is a subagents/agent-<id>.meta.json file.
type subagentMeta struct {
	ToolUseID string `json:"toolUseId"`
}

// readTurn reads the turn that starts at ref's user message from the
// session transcript under home and translates it. Messages of subagents are
// read from the session's subagent transcripts and merged in by time.
func readTurn(ctx context.Context, home, workDir string, ref turnRef) ([]message.Message, error) {
	path, err := transcriptPath(home, workDir, ref.SessionID)
	if err != nil {
		return nil, err
	}
	lines, err := readLines(path)
	if err != nil {
		return nil, err
	}
	turn, err := selectTurn(lines, ref.UserMessageUUID)
	if err != nil {
		return nil, fmt.Errorf("claude: %s: %w", path, err)
	}
	subagents, err := readSubagents(strings.TrimSuffix(path, ".jsonl"), turn)
	if err != nil {
		return nil, err
	}
	all := append(turn, subagents...)
	sort.SliceStable(all, func(i, j int) bool { return all[i].timestamp.Before(all[j].timestamp) })

	translator := newTranslator()
	var out []message.Message
	for _, l := range all {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("claude: read turn: %w", err)
		}
		msgs, err := translator.transcriptEntry(l.raw, l.parent, l.prompt)
		if err != nil {
			return nil, err
		}
		out = append(out, msgs...)
	}
	return out, nil
}

// transcriptPath finds <home>/projects/<encoded-workdir>/<session-id>.jsonl.
// Claude Code shortens long directory names, so when the encoded name does
// not exist the session file is looked up in every project directory.
func transcriptPath(home, workDir, sessionID string) (string, error) {
	name := sessionID + ".jsonl"
	for _, dir := range []string{resolve(workDir), workDir} {
		path := filepath.Join(home, "projects", EncodeWorkDir(dir), name)
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}
	matches, err := filepath.Glob(filepath.Join(home, "projects", "*", name))
	if err != nil {
		return "", fmt.Errorf("claude: find transcript: %w", err)
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("claude: no transcript for session %s under %s", sessionID, filepath.Join(home, "projects"))
	}
	return matches[0], nil
}

// readLines reads the JSON lines of a transcript file.
func readLines(path string) ([][]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("claude: read transcript: %w", err)
	}
	var out [][]byte
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(nil, len(data)+1)
	for sc.Scan() {
		if line := bytes.TrimSpace(sc.Bytes()); len(line) > 0 {
			out = append(out, append([]byte(nil), line...))
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("claude: read %s: %w", path, err)
	}
	return out, nil
}

// selectTurn returns the entries of the turn whose user message is userUUID:
// that message and everything after it up to the next turn's user message.
// Entries of one turn share the promptId of its user message.
func selectTurn(lines [][]byte, userUUID string) ([]transcriptLine, error) {
	var out []transcriptLine
	promptID := ""
	for _, raw := range lines {
		var h entryHead
		if json.Unmarshal(raw, &h) != nil {
			continue
		}
		if out == nil {
			if h.Type == "user" && h.UUID == userUUID {
				promptID = h.PromptID
				out = append(out, transcriptLine{raw: raw, timestamp: parseTime(h.Timestamp), prompt: true})
			}
			continue
		}
		if h.Type == "user" && h.PromptID != "" && h.PromptID != promptID {
			break
		}
		out = append(out, transcriptLine{raw: raw, timestamp: parseTime(h.Timestamp)})
	}
	if out == nil {
		return nil, fmt.Errorf("user message %s not found", userUUID)
	}
	return out, nil
}

// readSubagents reads the subagent transcripts of the calls made in turn,
// including those of nested subagents, from <session>/subagents/.
func readSubagents(sessionDir string, turn []transcriptLine) ([]transcriptLine, error) {
	dir := filepath.Join(sessionDir, "subagents")
	metas, err := filepath.Glob(filepath.Join(dir, "agent-*.meta.json"))
	if err != nil {
		return nil, fmt.Errorf("claude: find subagent transcripts: %w", err)
	}
	files := map[string]string{} // tool_use id -> transcript path
	for _, path := range metas {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("claude: read subagent meta: %w", err)
		}
		var m subagentMeta
		if err := json.Unmarshal(data, &m); err != nil {
			return nil, fmt.Errorf("claude: parse %s: %w", path, err)
		}
		if m.ToolUseID != "" {
			files[m.ToolUseID] = strings.TrimSuffix(path, ".meta.json") + ".jsonl"
		}
	}
	var out []transcriptLine
	pending := turn
	for len(pending) > 0 {
		var next []transcriptLine
		for _, id := range toolUseIDs(pending) {
			path, ok := files[id]
			if !ok {
				continue
			}
			delete(files, id)
			lines, err := readLines(path)
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, err
			}
			for _, raw := range lines {
				var h entryHead
				if json.Unmarshal(raw, &h) != nil {
					continue
				}
				next = append(next, transcriptLine{raw: raw, parent: id, timestamp: parseTime(h.Timestamp)})
			}
		}
		out = append(out, next...)
		pending = next
	}
	return out, nil
}

// toolUseIDs returns the ids of the tool_use blocks of the assistant entries
// in lines.
func toolUseIDs(lines []transcriptLine) []string {
	var ids []string
	for _, l := range lines {
		var h entryHead
		if json.Unmarshal(l.raw, &h) != nil || h.Type != "assistant" {
			continue
		}
		var blocks []block
		if json.Unmarshal(h.Message.Content, &blocks) != nil {
			continue
		}
		for _, b := range blocks {
			if b.Type == "tool_use" {
				ids = append(ids, b.ID)
			}
		}
	}
	return ids
}

func parseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t
}
