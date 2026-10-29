import { describe, expect, it } from "vitest";
import { buildItems, type Item, type ToolItem } from "@/lib/state/model";
import type { Message, TurnSummary } from "@/lib/api/types";

const turn = (status: TurnSummary["status"] = "running"): TurnSummary => ({
  id: "t1",
  sessionId: "s1",
  prompt: "Run the tests",
  status,
  startedAt: "2026-10-14T09:00:00.000Z",
  endedAt: status === "running" ? null : "2026-10-14T09:01:00.000Z",
  finalText: null,
  usage: null,
  error: null,
});

let n = 0;
const base = (kind: Message["kind"], extra: Partial<Message> = {}): Message => ({
  id: `m${++n}`,
  turnId: "t1",
  kind,
  receivedAt: "2026-10-14T09:00:01.000Z",
  truncated: false,
  ...extra,
});
const user = (text: string) => base("user", { text });
const assistant = (text: string, extra: Partial<Message> = {}) => base("assistant", { text, ...extra });
const call = (id: string, extra: Partial<Message> = {}) =>
  base("tool_call", {
    toolCall: {
      id,
      tool: "shell",
      rawName: "Bash",
      input: { type: "shell", command: "ls", description: "", timeoutMs: 0 },
    },
    ...extra,
  });
const subagentCall = (id: string) =>
  base("tool_call", {
    toolCall: {
      id,
      tool: "subagent",
      rawName: "Task",
      input: { type: "subagent", description: "Explore", prompt: "Find main", subagentType: "" },
    },
  });
const result = (callId: string, isError = false, extra: Partial<Message> = {}) =>
  base("tool_result", {
    toolResult: { callId, tool: "shell", isError, output: { type: "shell", output: "ok", exitCode: 0 } },
    ...extra,
  });

const kinds = (items: Item[]) => items.map((i) => i.kind);
const tools = (items: Item[]) => items.filter((i): i is ToolItem => i.kind === "tool");

describe("buildItems", () => {
  it("falls back to the prompt until the user message arrives", () => {
    const items = buildItems(turn(), []);
    expect(items).toEqual([{ kind: "user", id: "t1:prompt", text: "Run the tests", optimistic: true }]);
    const u = user("Run the tests");
    expect(buildItems(turn(), [u])[0]).toMatchObject({ kind: "user", id: u.id, optimistic: false });
  });

  it("de-duplicates by ID, keeping arrival order", () => {
    const u = user("hi");
    const a = assistant("one");
    const b = assistant("two");
    const items = buildItems(turn(), [u, a, a, b, a]);
    expect(items.map((i) => i.id)).toEqual([u.id, a.id, b.id]);
  });

  it("merges a call with its result and derives tool state", () => {
    const items = buildItems(turn(), [user("x"), call("c1"), result("c1"), call("c2"), result("c2", true), call("c3")]);
    expect(tools(items).map((t) => t.state)).toEqual(["succeeded", "failed", "running"]);
    expect(tools(items)[0].result?.callId).toBe("c1");
  });

  it("marks calls without a result in an ended turn as interrupted and adds a turn end", () => {
    const items = buildItems(turn("terminated"), [user("x"), call("c1")]);
    expect(tools(items)[0].state).toBe("interrupted");
    expect(kinds(items)).toEqual(["user", "tool", "turnEnd"]);
  });

  it("keeps a result without a call as its own tool item", () => {
    const items = buildItems(turn("completed"), [user("x"), result("orphan")]);
    expect(tools(items)[0].call).toBeUndefined();
    expect(tools(items)[0].state).toBe("succeeded");
  });

  it("nests messages under their subagent call", () => {
    const items = buildItems(turn(), [
      user("x"),
      subagentCall("sa"),
      call("inner", { parentToolUseId: "sa" }),
      result("inner", false, { parentToolUseId: "sa" }),
      assistant("inner text", { parentToolUseId: "sa" }),
      assistant("done"),
    ]);
    expect(kinds(items)).toEqual(["user", "tool", "assistant"]);
    const sa = tools(items)[0];
    expect(sa.state).toBe("running");
    expect(kinds(sa.children)).toEqual(["tool", "assistant"]);
    expect(tools(sa.children)[0].state).toBe("succeeded");
  });

  it("keeps messages with an unknown parent at the top level, marked orphaned", () => {
    const items = buildItems(turn(), [user("x"), assistant("lost", { parentToolUseId: "nope" })]);
    expect(items[1]).toMatchObject({ kind: "assistant", orphaned: true });
  });

  it("carries reasoning, including empty text", () => {
    const items = buildItems(turn(), [user("x"), base("reasoning", { text: "" })]);
    expect(items[1]).toMatchObject({ kind: "reasoning", text: "" });
  });
});
