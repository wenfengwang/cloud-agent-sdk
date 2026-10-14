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
	index := &index{dir: filepath.Join(home, ".cloudagent", "agents", agentID), agent: rec}
	if err := os.MkdirAll(index.dir, 0o700); err != nil {
		return nil, fmt.Errorf("cloudagent: create index: %w", err)
	}
	if err := index.writeAgent(); err != nil {
		return nil, err
	}
	if err := index.writeTurns(); err != nil {
		return nil, err
	}
	return index, nil
}

// startTurn appends a running turn and records the harness session.
func (i *index) startTurn(turnID string, ref json.RawMessage, sessionID string, startedAt time.Time) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if err := i.setSession(sessionID); err != nil {
		return err
	}
	i.turns = append(i.turns, turnRecord{TurnID: turnID, HarnessRef: ref, Status: statusRunning, StartedAt: startedAt.UTC()})
	if err := i.writeTurns(); err != nil {
		i.turns = i.turns[:len(i.turns)-1]
		return err
	}
	return nil
}

// endTurn records a turn's final reference and status.
func (i *index) endTurn(turnID string, ref json.RawMessage, sessionID, status string, endedAt time.Time) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if err := i.setSession(sessionID); err != nil {
		return err
	}
	ended := endedAt.UTC()
	for n := range i.turns {
		if i.turns[n].TurnID == turnID {
			i.turns[n].HarnessRef = ref
			i.turns[n].Status = status
			i.turns[n].EndedAt = &ended
		}
	}
	return i.writeTurns()
}

func (i *index) setSession(sessionID string) error {
	if sessionID == "" || sessionID == i.agent.HarnessSessionID {
		return nil
	}
	i.agent.HarnessSessionID = sessionID
	return i.writeAgent()
}

func (i *index) writeAgent() error {
	data, err := json.Marshal(i.agent)
	if err != nil {
		return fmt.Errorf("cloudagent: encode agent.json: %w", err)
	}
	return writeAtomic(filepath.Join(i.dir, "agent.json"), append(data, '\n'))
}

func (i *index) writeTurns() error {
	var buf bytes.Buffer
	for _, t := range i.turns {
		data, err := json.Marshal(t)
		if err != nil {
			return fmt.Errorf("cloudagent: encode turns.jsonl: %w", err)
		}
		buf.Write(data)
		buf.WriteByte('\n')
	}
	return writeAtomic(filepath.Join(i.dir, "turns.jsonl"), buf.Bytes())
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
