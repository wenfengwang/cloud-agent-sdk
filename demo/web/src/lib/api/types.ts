// The backend DTOs of demo design 001, one to one.

export type HarnessId = "claudecode" | "codex";
export type SessionStatus = "idle" | "starting" | "running" | "closing";
export type TurnStatus = "running" | "completed" | "terminated" | "cancelled" | "closed" | "failed";
export type MessageKind = "user" | "assistant" | "reasoning" | "tool_call" | "tool_result";
export type ToolName =
  | "shell"
  | "file_read"
  | "file_write"
  | "file_edit"
  | "file_patch"
  | "glob"
  | "grep"
  | "web_fetch"
  | "web_search"
  | "mcp"
  | "subagent"
  | "unknown";

export interface Gap {
  id: string;
  reason: string;
}

export interface HarnessInfo {
  harness: HarnessId;
  label: string;
  available: boolean;
  version: string;
  pinnedVersion: string;
  home: string;
  loginHint: string;
  defaultModel: string;
  modelSuggestions: string[];
}

export interface Config {
  displayName: string;
  harnesses: HarnessInfo[];
  homeMode: string;
  worktreeRoot: string;
  projectRoots: string[];
  debugRaw: boolean;
  gaps: Gap[];
}

export interface Project {
  path: string;
  name: string;
  isGitRepository: boolean;
  currentBranch: string;
}

export interface Worktree {
  path: string;
  branch: string;
  baseBranch: string;
}

export interface SessionSummary {
  id: string;
  harness: HarnessId;
  title: string;
  projectPath: string;
  projectName: string;
  workDir: string;
  model: string;
  worktree: Worktree | null;
  status: SessionStatus;
  activeTurnId: string | null;
  lastTurnStatus: TurnStatus | null;
  createdAt: string;
  lastActivityAt: string;
}

export interface Usage {
  inputTokens: number;
  outputTokens: number;
  cacheReadTokens: number;
  cacheWriteTokens: number;
}

export interface TurnSummary {
  id: string;
  sessionId: string;
  prompt: string;
  status: TurnStatus;
  startedAt: string;
  endedAt: string | null;
  finalText: string | null;
  usage: Usage | null;
  error: { message: string } | null;
}

export interface SessionDetail extends SessionSummary {
  turns: TurnSummary[];
}

export type ToolInput =
  | { type: "shell"; command: string; description: string; timeoutMs: number }
  | { type: "fileRead"; path: string; offset: number; limit: number }
  | { type: "fileWrite"; path: string; content: string }
  | { type: "fileEdit"; path: string; oldString: string; newString: string; replaceAll: boolean }
  | { type: "filePatch"; changes: { path: string; kind: string; diff: string }[] }
  | { type: "glob"; pattern: string; path: string }
  | { type: "grep"; pattern: string; path: string; glob: string }
  | { type: "webFetch"; url: string; prompt: string }
  | { type: "webSearch"; query: string }
  | { type: "mcp"; server: string; tool: string; arguments: unknown }
  | { type: "subagent"; description: string; prompt: string; subagentType: string }
  | { type: "unknown"; raw: unknown };

export type ToolOutput =
  | { type: "shell"; output: string; exitCode: number | null }
  | { type: "fileRead"; content: string }
  | { type: "text"; text: string }
  | { type: "mcp"; content: unknown }
  | { type: "unknown"; raw: unknown };

export interface ToolCall {
  id: string;
  tool: ToolName;
  rawName: string;
  input: ToolInput;
}

export interface ToolResult {
  callId: string;
  tool: ToolName;
  isError: boolean;
  output: ToolOutput;
}

export interface Message {
  id: string;
  turnId: string;
  parentToolUseId?: string;
  kind: MessageKind;
  receivedAt: string;
  text?: string;
  toolCall?: ToolCall;
  toolResult?: ToolResult;
  truncated: boolean;
  raw?: unknown;
}

export interface MessageList {
  messages: Message[];
  error: { message: string } | null;
}

export interface ChangedFile {
  path: string;
  status: "added" | "modified" | "deleted" | "renamed";
  additions: number;
  deletions: number;
  diff: string;
}

export interface Changes {
  baseCommit: string;
  files: ChangedFile[];
}

export interface DeleteResult {
  worktree: { path: string; removed: boolean; reason?: string } | null;
}

export interface CreateSession {
  harness: HarnessId;
  workDir: string;
  model: string;
  worktree?: { enabled: boolean; baseBranch: string };
}
