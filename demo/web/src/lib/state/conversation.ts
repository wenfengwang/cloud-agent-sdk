import type { Message, SessionDetail, SessionSummary, TurnSummary } from "@/lib/api/types";

export interface ConversationState {
  status: "loading" | "ready" | "notFound" | "error";
  error: string | null;
  turns: TurnSummary[];
  messages: Record<string, Message[]>;
  streamErrors: Record<string, string>;
  /** The prompt being started, shown optimistically until Turn returns. */
  pending: { prompt: string } | null;
  sendError: string | null;
  /** A prompt to put back into the composer after a failed start. */
  restoredPrompt: string | null;
  /** The turn this tab started, until its turn.ended arrives. */
  awaitingEnd: string | null;
}

export const initialConversation: ConversationState = {
  status: "loading",
  error: null,
  turns: [],
  messages: {},
  streamErrors: {},
  pending: null,
  sendError: null,
  restoredPrompt: null,
  awaitingEnd: null,
};

export type ConversationAction =
  | { type: "reset" }
  | { type: "loaded"; detail: SessionDetail }
  | { type: "notFound" }
  | { type: "loadFailed"; message: string }
  | { type: "historyLoaded"; turnId: string; messages: Message[]; error: string | null }
  | { type: "message"; turnId: string; message: Message }
  | { type: "streamError"; turnId: string; message: string }
  | { type: "turnEnded"; turn: TurnSummary }
  | { type: "sendStarted"; prompt: string }
  | { type: "sendSucceeded"; turn: TurnSummary }
  | { type: "sendFailed"; prompt: string; message: string | null }
  | { type: "promptRestored" };

function upsertTurn(turns: TurnSummary[], turn: TurnSummary): TurnSummary[] {
  const i = turns.findIndex((t) => t.id === turn.id);
  if (i < 0) return [...turns, turn];
  // An ended summary is never replaced by a running one.
  if (turns[i].status !== "running" && turn.status === "running") return turns;
  const next = turns.slice();
  next[i] = turn;
  return next;
}

export function conversationReducer(state: ConversationState, action: ConversationAction): ConversationState {
  switch (action.type) {
    case "reset":
      return initialConversation;
    case "loaded": {
      let turns = state.turns;
      for (const t of action.detail.turns) turns = upsertTurn(turns, t);
      const order = new Map(action.detail.turns.map((t, i) => [t.id, i]));
      turns = turns.slice().sort((a, b) => (order.get(a.id) ?? 1e9) - (order.get(b.id) ?? 1e9));
      return { ...state, status: "ready", error: null, turns };
    }
    case "notFound":
      return { ...state, status: "notFound" };
    case "loadFailed":
      return { ...state, status: "error", error: action.message };
    case "historyLoaded": {
      const known = new Set((state.messages[action.turnId] ?? []).map((m) => m.id));
      const merged = [...action.messages.filter((m) => !known.has(m.id)), ...(state.messages[action.turnId] ?? [])];
      return {
        ...state,
        messages: { ...state.messages, [action.turnId]: merged },
        streamErrors: action.error ? { ...state.streamErrors, [action.turnId]: action.error } : state.streamErrors,
      };
    }
    case "message": {
      const list = state.messages[action.turnId] ?? [];
      if (list.some((m) => m.id === action.message.id)) return state;
      return { ...state, messages: { ...state.messages, [action.turnId]: [...list, action.message] } };
    }
    case "streamError":
      return { ...state, streamErrors: { ...state.streamErrors, [action.turnId]: action.message } };
    case "turnEnded":
      return {
        ...state,
        turns: upsertTurn(state.turns, action.turn),
        awaitingEnd: state.awaitingEnd === action.turn.id ? null : state.awaitingEnd,
      };
    case "sendStarted":
      return { ...state, pending: { prompt: action.prompt }, sendError: null, restoredPrompt: null };
    case "sendSucceeded":
      return {
        ...state,
        pending: null,
        turns: upsertTurn(state.turns, action.turn),
        // The turn may already have ended through the event stream.
        awaitingEnd: state.turns.some((t) => t.id === action.turn.id && t.status !== "running") ? null : action.turn.id,
      };
    case "sendFailed":
      return { ...state, pending: null, sendError: action.message, restoredPrompt: action.prompt };
    case "promptRestored":
      return { ...state, restoredPrompt: null };
  }
}

/**
 * The composer is locked while a send is pending, while the session is not
 * idle, and until the turn this tab started has its turn.ended.
 */
export function isLocked(state: ConversationState, session: SessionSummary | undefined): boolean {
  if (state.pending || state.awaitingEnd) return true;
  return session !== undefined && session.status !== "idle";
}
