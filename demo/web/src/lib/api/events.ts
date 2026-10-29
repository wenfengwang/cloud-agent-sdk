import type { Message, PartialText, SessionSummary, TurnSummary } from "./types";

/** The part of EventSource the client uses, so tests can inject a fake. */
export interface EventSourceLike {
  readyState: number;
  onerror: (() => void) | null;
  addEventListener(name: string, fn: (e: { data: string }) => void): void;
  close(): void;
}

const CLOSED = 2;

/** Receives one turn's events. lastMessageId is the replay position. */
export interface TurnHandlers {
  onMessage(message: Message): void;
  onPartial?(partial: PartialText): void;
  onStreamError(message: string): void;
  onEnded(turn: TurnSummary): void;
  lastMessageId(): string;
  onSubscribeError?(err: unknown): void;
}

export type SessionChange = { type: "upserted"; session: SessionSummary } | { type: "deleted"; id: string };

export type ConnectionState = "connecting" | "open" | "lost";

export interface EventsClientOptions {
  url?: string;
  createSource?: (url: string) => EventSourceLike;
  subscribe(connectionId: string, sessionId: string, turnId: string, afterMessageId: string): Promise<void>;
  unsubscribe(connectionId: string, turnId: string): Promise<void>;
  /** How long a dropped connection may reconnect before it counts as lost. */
  lostAfterMs?: number;
}

interface Subscription {
  sessionId: string;
  handlers: TurnHandlers;
}

/**
 * The tab's one EventSource. It learns its connection ID from hello, posts
 * turn subscriptions, routes turn events by turnId, and resubscribes every
 * live subscription from its last message ID after a reconnect.
 */
export class EventsClient {
  private source: EventSourceLike | null = null;
  private connectionId = "";
  private subs = new Map<string, Subscription>();
  private sessionFns = new Set<(c: SessionChange) => void>();
  private helloFns = new Set<() => void>();
  private stateFns = new Set<(s: ConnectionState) => void>();
  private state: ConnectionState = "connecting";
  private lostTimer: ReturnType<typeof setTimeout> | null = null;
  private hellos = 0;

  constructor(private readonly opts: EventsClientOptions) {}

  start(): void {
    this.open();
  }

  close(): void {
    this.clearLostTimer();
    this.source?.close();
    this.source = null;
  }

  /** Closes the current source and opens a new one. */
  reconnect(): void {
    this.close();
    this.open();
  }

  getState(): ConnectionState {
    return this.state;
  }

  onSession(fn: (c: SessionChange) => void): () => void {
    this.sessionFns.add(fn);
    return () => this.sessionFns.delete(fn);
  }

  /** Called on every hello after the first, that is, after a reconnect. */
  onHello(fn: () => void): () => void {
    this.helloFns.add(fn);
    return () => this.helloFns.delete(fn);
  }

  onState(fn: (s: ConnectionState) => void): () => void {
    this.stateFns.add(fn);
    return () => this.stateFns.delete(fn);
  }

  /**
   * Follows one turn until turn.ended. Subscribing to a followed turn
   * replaces its handlers. The returned function stops following.
   */
  subscribe(sessionId: string, turnId: string, handlers: TurnHandlers): () => void {
    const known = this.subs.has(turnId);
    const sub = { sessionId, handlers };
    this.subs.set(turnId, sub);
    if (!known && this.connectionId) this.post(turnId, sub);
    return () => {
      if (this.subs.get(turnId) !== sub) return;
      this.subs.delete(turnId);
      if (this.connectionId) this.opts.unsubscribe(this.connectionId, turnId).catch(() => {});
    };
  }

  private post(turnId: string, sub: Subscription): void {
    const connectionId = this.connectionId;
    this.opts.subscribe(connectionId, sub.sessionId, turnId, sub.handlers.lastMessageId()).catch((err) => {
      // A failure on a connection that was since replaced is retried by
      // the next hello.
      if (connectionId === this.connectionId) sub.handlers.onSubscribeError?.(err);
    });
  }

  private open(): void {
    const create = this.opts.createSource ?? ((url: string) => new EventSource(url) as unknown as EventSourceLike);
    const source = create(this.opts.url ?? "/api/events");
    this.source = source;
    this.connectionId = "";
    this.setState("connecting");

    const on = (name: string, fn: (data: unknown) => void) =>
      source.addEventListener(name, (e) => {
        if (this.source !== source) return;
        let data: unknown;
        try {
          data = JSON.parse(e.data);
        } catch {
          return;
        }
        fn(data);
      });

    on("hello", (data) => {
      this.connectionId = (data as { connectionId: string }).connectionId;
      this.clearLostTimer();
      this.setState("open");
      for (const [turnId, sub] of this.subs) this.post(turnId, sub);
      if (this.hellos++ > 0) for (const fn of this.helloFns) fn();
    });
    on("session.upserted", (data) => {
      for (const fn of this.sessionFns) fn({ type: "upserted", session: data as SessionSummary });
    });
    on("session.deleted", (data) => {
      for (const fn of this.sessionFns) fn({ type: "deleted", id: (data as { id: string }).id });
    });
    on("message", (data) => {
      const { turnId, message } = data as { turnId: string; message: Message };
      this.subs.get(turnId)?.handlers.onMessage(message);
    });
    on("message.partial", (data) => {
      const partial = data as PartialText;
      this.subs.get(partial.turnId)?.handlers.onPartial?.(partial);
    });
    on("stream.error", (data) => {
      const { turnId, message } = data as { turnId: string; message: string };
      this.subs.get(turnId)?.handlers.onStreamError(message);
    });
    on("turn.ended", (data) => {
      const turn = data as TurnSummary;
      const sub = this.subs.get(turn.id);
      if (!sub) return;
      this.subs.delete(turn.id);
      sub.handlers.onEnded(turn);
    });

    source.onerror = () => {
      if (this.source !== source) return;
      this.connectionId = "";
      if (source.readyState === CLOSED) {
        this.clearLostTimer();
        this.setState("lost");
        return;
      }
      // EventSource reconnects on its own; report it only if that takes long.
      if (!this.lostTimer) {
        this.lostTimer = setTimeout(() => {
          this.lostTimer = null;
          if (this.source === source && !this.connectionId) this.setState("lost");
        }, this.opts.lostAfterMs ?? 3000);
      }
    };
  }

  private clearLostTimer(): void {
    if (this.lostTimer) clearTimeout(this.lostTimer);
    this.lostTimer = null;
  }

  private setState(s: ConnectionState): void {
    if (this.state === s) return;
    this.state = s;
    for (const fn of this.stateFns) fn(s);
  }
}
