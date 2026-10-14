// Package fakeharness scripts a fake harness executable for tests. The
// executable (./cmd/fakeharness) is built once by Build and installed as both
// claude and codex; it replays recorded traffic and can be told to hang,
// crash, reject the start, and honor or ignore interrupts. The SDK reaches it
// through PATH exactly as it reaches the real CLIs. See
// design/002-testing.md.
package fakeharness

import "encoding/json"

// EnvScript names the environment variable that holds the script path. The
// fake inherits it from the test process through the SDK.
const EnvScript = "CLOUDAGENT_FAKE_SCRIPT"

// Traffic directions.
const (
	DirSend = "send" // written by the adapter to the harness's stdin
	DirRecv = "recv" // written by the harness to its stdout
)

// TrafficLine is one line of a traffic-<n>.jsonl fixture.
type TrafficLine struct {
	Dir  string          `json:"dir"`
	Line json.RawMessage `json:"line"`
}

// Script configures every invocation of the fake during one test.
type Script struct {
	// StateDir receives what the fake observed; see State.
	StateDir string `json:"stateDir"`
	// Substitutions maps placeholders (for example "@WORKDIR@") to values.
	// They are applied to every recv line, file content, and file
	// destination. Values are inserted JSON-string-escaped.
	Substitutions map[string]string `json:"substitutions,omitempty"`
	// ArgSubstitutions maps a placeholder to command-line flags; the value
	// that follows the first of those flags present in the fake's arguments
	// (as "--flag value" or "--flag=value") replaces the placeholder like a
	// Substitution. Example: {"@SESSION_ID@": ["--session-id", "--resume"]}.
	ArgSubstitutions map[string][]string `json:"argSubstitutions,omitempty"`
	// Invocations scripts each start of the fake, in order: the n-th process
	// started (claude or codex alike) uses Invocations[n-1]. An invocation
	// beyond the list exits with status 2.
	Invocations []Invocation `json:"invocations"`
}

// Invocation scripts one process of the fake.
//
// Replay walks Traffic in order. A recv line is written to stdout after
// substitution; a Codex response has its id rewritten to the id of the
// matching actual request, and a Claude control_response its request_id. A
// send line blocks until stdin delivers a line of the same kind (Claude:
// type, plus the subtype of a control_request; Codex: the JSON-RPC method,
// or "response"); stdin lines of other kinds are recorded and skipped.
//
// SIGTERM, SIGINT, and stdin EOF end the process (status 143, 130, and 0),
// unless a HangAfter with OnInterrupt "ignore" is in effect. When the
// traffic is exhausted, Files are written; the process then exits if
// ExitAtEnd is set and otherwise waits for stdin EOF or a signal.
type Invocation struct {
	// Traffic is the path of a traffic-<n>.jsonl file. Empty replays
	// nothing.
	Traffic string `json:"traffic,omitempty"`
	// Files are written when the traffic is exhausted, so that a finished
	// turn can be read back from the harness home.
	Files []File `json:"files,omitempty"`
	// PaceMs delays every recv line.
	PaceMs int `json:"paceMs,omitempty"`
	// ExitAtEnd exits with status 0 once the traffic is exhausted.
	ExitAtEnd bool `json:"exitAtEnd,omitempty"`
	// ExitAtStart exits before reading stdin (Claude rejecting a start).
	ExitAtStart *Exit `json:"exitAtStart,omitempty"`
	// RejectMethod answers the first request with this JSON-RPC method
	// with an error response instead of replaying it (Codex rejecting a
	// start, for example "turn/start").
	RejectMethod string `json:"rejectMethod,omitempty"`
	// CrashAfter exits once that many recv lines were written.
	CrashAfter *Crash `json:"crashAfter,omitempty"`
	// HangAfter stops replaying once that many recv lines were written.
	HangAfter *Hang `json:"hangAfter,omitempty"`
}

// File is a fixture file the fake writes. From is read as is; To is an
// absolute path after substitution.
type File struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Exit is an immediate exit.
type Exit struct {
	Code   int    `json:"code"`
	Stderr string `json:"stderr,omitempty"`
}

// Crash exits with ExitCode after Lines recv lines.
type Crash struct {
	Lines    int `json:"lines"`
	ExitCode int `json:"exitCode"`
}

// Interrupt handling while hanging.
const (
	// OnInterruptHonor resumes the replay when an interrupt request (Claude:
	// control_request interrupt; Codex: turn/interrupt) arrives, which then
	// satisfies the next send line; signals and stdin EOF end the process.
	OnInterruptHonor = "honor"
	// OnInterruptIgnore ignores interrupt requests, SIGTERM, SIGINT, and
	// stdin EOF; only SIGKILL ends the process.
	OnInterruptIgnore = "ignore"
)

// Hang stops the replay after Lines recv lines.
type Hang struct {
	Lines       int    `json:"lines"`
	OnInterrupt string `json:"onInterrupt"`
	// SpawnChild starts a long-running child process in its own process
	// group when the hang begins, as a harness's tool command would; its
	// process ID is recorded (see State.Children).
	SpawnChild bool `json:"spawnChild,omitempty"`
}

// Info is what the fake records about its own process.
type Info struct {
	Args []string          `json:"args"` // os.Args[1:]
	Dir  string            `json:"dir"`  // working directory
	Env  map[string]string `json:"env"`  // HOME, CLAUDE_CONFIG_DIR, CODEX_HOME, when set
}
