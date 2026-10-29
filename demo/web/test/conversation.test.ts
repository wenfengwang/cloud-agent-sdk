import { describe, expect, it } from "vitest";
import { conversationReducer, initialConversation, isLocked, type ConversationState } from "@/lib/state/conversation";
import type { Message, SessionDetail, SessionSummary, TurnSummary } from "@/lib/api/types";

const summary = (over: Partial<SessionSummary> = {}): SessionSummary => ({
  id: "s1",
  harness: "codex",
  title: "",
  projectPath: "/home/alex/code/storefront",
  projectName: "storefront",
  workDir: "/home/alex/code/storefront",
  model: "",
  worktree: null,
  status: "idle",
  activeTurnId: null,
  lastTurnStatus: null,
  createdAt: "2026-10-14T09:00:00.000Z",
  lastActivityAt: "2026-10-14T09:00:00.000Z",
  ...over,
});
const turn = (id: string, status: TurnSummary["status"] = "running"): TurnSummary => ({
  id,
  sessionId: "s1",
  prompt: "go",
  status,
  startedAt: "2026-10-14T09:00:00.000Z",
  endedAt: status === "running" ? null : "2026-10-14T09:01:00.000Z",
  finalText: null,
  usage: null,
  error: null,
});
const detail = (turns: TurnSummary[] = []): SessionDetail => ({ ...summary(), turns });
const msg = (id: string, turnId = "t1"): Message => ({
  id,
  turnId,
  kind: "assistant",
  text: id,
  receivedAt: "2026-10-14T09:00:00.000Z",
  truncated: false,
});

const run = (state: ConversationState, ...actions: Parameters<typeof conversationReducer>[1][]) =>
  actions.reduce(conversationReducer, state);

describe("conversation reducer", () => {
  it("unlocks only when the session is idle and the started turn ended", () => {
    let s = run(initialConversation, { type: "loaded", detail: detail() }, { type: "sendStarted", prompt: "go" });
    expect(isLocked(s, summary())).toBe(true);
    expect(s.pending?.prompt).toBe("go");

    s = run(s, { type: "sendSucceeded", turn: turn("t1") });
    expect(s.pending).toBeNull();
    expect(s.turns.map((t) => t.id)).toEqual(["t1"]);
    expect(isLocked(s, summary({ status: "running", activeTurnId: "t1" }))).toBe(true);
    // Idle alone does not unlock: turn.ended has not arrived.
    expect(isLocked(s, summary({ status: "idle" }))).toBe(true);

    s = run(s, { type: "turnEnded", turn: turn("t1", "completed") });
    expect(isLocked(s, summary({ status: "running" }))).toBe(true);
    expect(isLocked(s, summary({ status: "idle" }))).toBe(false);
    expect(s.turns[0].status).toBe("completed");
  });

  it("restores the prompt and shows the error on a start error", () => {
    const s = run(
      initialConversation,
      { type: "loaded", detail: detail() },
      { type: "sendStarted", prompt: "go" },
      { type: "sendFailed", prompt: "go", message: "claude: not found" },
    );
    expect(s.pending).toBeNull();
    expect(s.sendError).toBe("claude: not found");
    expect(s.restoredPrompt).toBe("go");
    expect(isLocked(s, summary())).toBe(false);
  });

  it("restores the prompt without an error when the start was cancelled", () => {
    const s = run(
      initialConversation,
      { type: "loaded", detail: detail() },
      { type: "sendStarted", prompt: "go" },
      { type: "sendFailed", prompt: "go", message: null },
    );
    expect(s.sendError).toBeNull();
    expect(s.restoredPrompt).toBe("go");
  });

  it("de-duplicates messages across a resubscribe", () => {
    const s = run(
      initialConversation,
      { type: "loaded", detail: detail([turn("t1")]) },
      { type: "message", turnId: "t1", message: msg("a") },
      { type: "message", turnId: "t1", message: msg("b") },
      { type: "message", turnId: "t1", message: msg("b") },
    );
    expect(s.messages.t1.map((m) => m.id)).toEqual(["a", "b"]);
  });

  it("records stream errors per turn", () => {
    const s = run(
      initialConversation,
      { type: "loaded", detail: detail([turn("t1")]) },
      { type: "streamError", turnId: "t1", message: "unreadable" },
    );
    expect(s.streamErrors.t1).toBe("unreadable");
  });

  it("locks for a turn started in another tab and merges it on reload", () => {
    let s = run(initialConversation, { type: "loaded", detail: detail([turn("t0", "completed")]) });
    const other = summary({ status: "running", activeTurnId: "t9" });
    expect(isLocked(s, other)).toBe(true);
    s = run(s, { type: "loaded", detail: { ...detail([turn("t0", "completed"), turn("t9")]) } });
    expect(s.turns.map((t) => t.id)).toEqual(["t0", "t9"]);
    // A reload keeps a turn end already received.
    s = run(
      s,
      { type: "turnEnded", turn: turn("t9", "completed") },
      { type: "loaded", detail: detail([turn("t0", "completed"), turn("t9")]) },
    );
    expect(s.turns[1].status).toBe("completed");
  });

  it("loads history for ended turns", () => {
    const s = run(
      initialConversation,
      { type: "loaded", detail: detail([turn("t1", "completed")]) },
      { type: "historyLoaded", turnId: "t1", messages: [msg("a")], error: "partial" },
    );
    expect(s.messages.t1).toHaveLength(1);
    expect(s.streamErrors.t1).toBe("partial");
  });

  it("locks while the session is closing", () => {
    const s = run(initialConversation, { type: "loaded", detail: detail() });
    expect(isLocked(s, summary({ status: "closing" }))).toBe(true);
  });
});
