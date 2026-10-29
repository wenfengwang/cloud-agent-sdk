import type { Message, ToolCall, ToolResult, TurnSummary } from "@/lib/api/types";

export type ToolState = "running" | "succeeded" | "failed" | "interrupted";

export interface UserItem {
  kind: "user";
  id: string;
  text: string;
  optimistic: boolean;
}

export interface TextItem {
  kind: "assistant" | "reasoning";
  id: string;
  text: string;
  truncated: boolean;
  orphaned?: boolean;
}

export interface ToolItem {
  kind: "tool";
  id: string;
  call?: ToolCall;
  result?: ToolResult;
  state: ToolState;
  truncated: boolean;
  children: Item[];
  orphaned?: boolean;
}

export interface TurnEndItem {
  kind: "turnEnd";
  id: string;
  turn: TurnSummary;
}

export type Item = UserItem | TextItem | ToolItem | TurnEndItem;

/**
 * Folds one turn's messages into items: de-duplicated by ID in arrival
 * order, calls merged with their results, messages nested under their
 * subagent call, and a turn end once the turn has ended.
 */
export function buildItems(turn: TurnSummary, messages: Message[]): Item[] {
  const top: Item[] = [];
  const toolsByCallId = new Map<string, ToolItem>();
  const seen = new Set<string>();
  let hasUser = false;

  for (const m of messages) {
    if (seen.has(m.id)) continue;
    seen.add(m.id);

    let container = top;
    let orphaned = false;
    if (m.parentToolUseId) {
      const parent = toolsByCallId.get(m.parentToolUseId);
      if (parent && parent.call?.tool === "subagent") container = parent.children;
      else orphaned = true;
    }

    switch (m.kind) {
      case "user":
        if (container === top) hasUser = true;
        container.push({ kind: "user", id: m.id, text: m.text ?? "", optimistic: false });
        break;
      case "assistant":
      case "reasoning":
        container.push({
          kind: m.kind,
          id: m.id,
          text: m.text ?? "",
          truncated: m.truncated,
          ...(orphaned ? { orphaned } : {}),
        });
        break;
      case "tool_call": {
        if (!m.toolCall) break;
        const item: ToolItem = {
          kind: "tool",
          id: m.id,
          call: m.toolCall,
          state: "running",
          truncated: m.truncated,
          children: [],
          ...(orphaned ? { orphaned } : {}),
        };
        toolsByCallId.set(m.toolCall.id, item);
        container.push(item);
        break;
      }
      case "tool_result": {
        if (!m.toolResult) break;
        const existing = toolsByCallId.get(m.toolResult.callId);
        if (existing) {
          existing.result = m.toolResult;
          existing.truncated ||= m.truncated;
        } else {
          container.push({
            kind: "tool",
            id: m.id,
            result: m.toolResult,
            state: "running",
            truncated: m.truncated,
            children: [],
          });
        }
        break;
      }
    }
  }

  const running = turn.status === "running";
  const settle = (items: Item[]) => {
    for (const item of items) {
      if (item.kind !== "tool") continue;
      if (item.result) item.state = item.result.isError ? "failed" : "succeeded";
      else item.state = running ? "running" : "interrupted";
      settle(item.children);
    }
  };
  settle(top);

  if (!hasUser) top.unshift({ kind: "user", id: `${turn.id}:prompt`, text: turn.prompt, optimistic: true });
  if (!running) top.push({ kind: "turnEnd", id: `${turn.id}:end`, turn });
  return top;
}
