package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Turn statuses written to turns.jsonl.
const (
	statusRunning    = "running"
	statusCompleted  = "completed"
	statusTerminated = "terminated"
	statusCancelled  = "cancelled"
	statusClosed     = "closed"
	statusFailed     = "failed"
)

type agentRecord struct {
	Harness          string `json:"harness"`
	Model            string `json:"model"`
	WorkDir          string `json:"workdir"`
	Home             string `json:"home"`
	HarnessSessionID string `json:"harness_session_id"`
}

type turnRecord struct {
	TurnID     string          `json:"turn_id"`
	HarnessRef json.RawMessage `json:"harness_ref"`
	Status     string          `json:"status"`
	StartedAt  time.Time       `json:"started_at"`
	EndedAt    *time.Time      `json:"ended_at"`
}

// index is one agent's entry under ~/.cloudagent/agents/<agent-id>/. It holds
// IDs, references, and status only, never message content. Both files are
// rewritten atomically on every change.
type index struct {
	dir string

	mu    sync.Mutex
	agent agentRecord
	turns []turnRecord
}

func createIndex(agentID string, rec agentRecord) (*index, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("cloudagent: locate home directory: %w", err)
	}
	ix := &index{dir: filepath.Join(home, ".cloudagent", "agents", agentID), agent: rec}
	if err := os.MkdirAll(ix.dir, 0o700); err != nil {
		return nil, fmt.Errorf("cloudagent: create index: %w", err)
	}
	if err := ix.writeAgent(); err != nil {
		return nil, err
	}
	if err := ix.writeTurns(); err != nil {
		return nil, err
	}
	return ix, nil
}

// startTurn appends a running turn and records the harness session.
func (ix *index) startTurn(turnID string, ref json.RawMessage, sessionID string, startedAt time.Time) error {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	if err := ix.setSession(sessionID); err != nil {
		return err
	}
	ix.turns = append(ix.turns, turnRecord{TurnID: turnID, HarnessRef: ref, Status: statusRunning, StartedAt: startedAt.UTC()})
	if err := ix.writeTurns(); err != nil {
		ix.turns = ix.turns[:len(ix.turns)-1]
		return err
	}
	return nil
}

// endTurn records a turn's final reference and status.
func (ix *index) endTurn(turnID string, ref json.RawMessage, sessionID, status string, endedAt time.Time) error {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	if err := ix.setSession(sessionID); err != nil {
		return err
	}
	ended := endedAt.UTC()
	for i := range ix.turns {
		if ix.turns[i].TurnID == turnID {
			ix.turns[i].HarnessRef = ref
			ix.turns[i].Status = status
			ix.turns[i].EndedAt = &ended
		}
	}
	return ix.writeTurns()
}

func (ix *index) setSession(sessionID string) error {
	if sessionID == "" || sessionID == ix.agent.HarnessSessionID {
		return nil
	}
	ix.agent.HarnessSessionID = sessionID
	return ix.writeAgent()
}

func (ix *index) writeAgent() error {
	data, err := json.Marshal(ix.agent)
	if err != nil {
		return fmt.Errorf("cloudagent: encode agent.json: %w", err)
	}
	return writeAtomic(filepath.Join(ix.dir, "agent.json"), append(data, '\n'))
}

func (ix *index) writeTurns() error {
	var buf bytes.Buffer
	for _, t := range ix.turns {
		data, err := json.Marshal(t)
		if err != nil {
			return fmt.Errorf("cloudagent: encode turns.jsonl: %w", err)
		}
		buf.Write(data)
		buf.WriteByte('\n')
	}
	return writeAtomic(filepath.Join(ix.dir, "turns.jsonl"), buf.Bytes())
}

// writeAtomic replaces path with data through a temporary file and a rename.
func writeAtomic(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("cloudagent: write index: %w", err)
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(data); err != nil {
		f.Close()
		return fmt.Errorf("cloudagent: write %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("cloudagent: write %s: %w", path, err)
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return fmt.Errorf("cloudagent: write %s: %w", path, err)
	}
	return nil
}
