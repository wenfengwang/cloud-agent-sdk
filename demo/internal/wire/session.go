package wire

// CreateSession is the body of POST /api/sessions.
type CreateSession struct {
	Harness  string          `json:"harness"`
	WorkDir  string          `json:"workDir"`
	Model    string          `json:"model"`
	Worktree *WorktreeOption `json:"worktree"`
}

// WorktreeOption asks for a new worktree based on BaseBranch, the current
// branch when empty.
type WorktreeOption struct {
	Enabled    bool   `json:"enabled"`
	BaseBranch string `json:"baseBranch"`
}

// Worktree is a worktree the backend created for a session.
type Worktree struct {
	Path       string `json:"path"`
	Branch     string `json:"branch"`
	BaseBranch string `json:"baseBranch"`
}

// SessionSummary is a session as the sidebar sees it.
type SessionSummary struct {
	ID             string    `json:"id"`
	Harness        string    `json:"harness"`
	Title          string    `json:"title"`
	ProjectPath    string    `json:"projectPath"`
	ProjectName    string    `json:"projectName"`
	WorkDir        string    `json:"workDir"`
	Model          string    `json:"model"`
	Worktree       *Worktree `json:"worktree"`
	Status         string    `json:"status"`
	ActiveTurnID   *string   `json:"activeTurnId"`
	LastTurnStatus *string   `json:"lastTurnStatus"`
	CreatedAt      Time      `json:"createdAt"`
	LastActivityAt Time      `json:"lastActivityAt"`
}

// SessionDetail is a SessionSummary plus its turns in start order.
type SessionDetail struct {
	SessionSummary
	Turns []TurnSummary `json:"turns"`
}

// TurnSummary is one turn of a session.
type TurnSummary struct {
	ID        string        `json:"id"`
	SessionID string        `json:"sessionId"`
	Prompt    string        `json:"prompt"`
	Status    string        `json:"status"`
	StartedAt Time          `json:"startedAt"`
	EndedAt   *Time         `json:"endedAt"`
	FinalText *string       `json:"finalText"`
	Usage     *Usage        `json:"usage"`
	Error     *ErrorMessage `json:"error"`
}

// ErrorMessage is a human-readable error attached to a turn or a message
// list.
type ErrorMessage struct {
	Message string `json:"message"`
}

// DeleteResult is the response of DELETE /api/sessions/{id}.
type DeleteResult struct {
	Worktree *WorktreeRemoval `json:"worktree"`
}

// WorktreeRemoval says whether a session's worktree was removed.
type WorktreeRemoval struct {
	Path    string `json:"path"`
	Removed bool   `json:"removed"`
	Reason  string `json:"reason,omitempty"`
}

// MessageList is the record of an ended turn.
type MessageList struct {
	Messages []Message     `json:"messages"`
	Error    *ErrorMessage `json:"error"`
}
