import type {
  Changes,
  Config,
  CreateSession,
  DeleteResult,
  MessageList,
  Project,
  SessionDetail,
  SessionSummary,
  TurnSummary,
} from "./types";

/** An error response of the backend: {"error": {"code", "message"}}. */
export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    message: string,
  ) {
    super(message);
    this.name = "ApiError";
  }
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const init: RequestInit = { method, headers: {} };
  if (method !== "GET") {
    // The backend requires a JSON content type on every mutating request.
    init.headers = { "Content-Type": "application/json" };
    init.body = body === undefined ? "{}" : JSON.stringify(body);
  }
  let res: Response;
  try {
    res = await fetch(`/api${path}`, init);
  } catch (err) {
    throw new ApiError(0, "network", `The backend is unreachable: ${String(err)}`);
  }
  if (res.status === 204) return undefined as T;
  const text = await res.text();
  let data: unknown = undefined;
  try {
    data = text ? JSON.parse(text) : undefined;
  } catch {
    // Not JSON; handled below.
  }
  if (!res.ok) {
    const err = (data as { error?: { code?: string; message?: string } } | undefined)?.error;
    throw new ApiError(res.status, err?.code ?? "http_error", err?.message ?? `HTTP ${res.status}`);
  }
  return data as T;
}

const enc = encodeURIComponent;

export const api = {
  config: () => request<Config>("GET", "/config"),
  projects: () => request<{ projects: Project[] }>("GET", "/projects").then((r) => r.projects),
  branches: (path: string) =>
    request<{ current: string; branches: string[] }>("GET", `/projects/branches?path=${enc(path)}`),
  sessions: () => request<{ sessions: SessionSummary[] }>("GET", "/sessions").then((r) => r.sessions),
  createSession: (req: CreateSession) => request<SessionDetail>("POST", "/sessions", req),
  session: (id: string) => request<SessionDetail>("GET", `/sessions/${enc(id)}`),
  deleteSession: (id: string) => request<DeleteResult>("DELETE", `/sessions/${enc(id)}`),
  changes: (id: string) => request<Changes>("GET", `/sessions/${enc(id)}/changes`),
  startTurn: (id: string, prompt: string) => request<TurnSummary>("POST", `/sessions/${enc(id)}/turns`, { prompt }),
  stop: (id: string) => request<object>("POST", `/sessions/${enc(id)}/stop`),
  messages: (sessionId: string, turnId: string) =>
    request<MessageList>("GET", `/sessions/${enc(sessionId)}/turns/${enc(turnId)}/messages`),
  subscribe: (connectionId: string, sessionId: string, turnId: string, afterMessageId: string) =>
    request<void>("POST", `/events/${enc(connectionId)}/subscriptions`, {
      sessionId,
      turnId,
      afterMessageId,
    }),
  unsubscribe: (connectionId: string, turnId: string) =>
    request<void>("DELETE", `/events/${enc(connectionId)}/subscriptions/${enc(turnId)}`),
};

export type Api = typeof api;
