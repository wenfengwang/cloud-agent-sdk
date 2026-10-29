import { describe, expect, it, vi } from "vitest";
import { EventsClient, type EventSourceLike, type TurnHandlers } from "@/lib/api/events";
import type { Message, PartialText, TurnSummary } from "@/lib/api/types";

class FakeSource implements EventSourceLike {
  static readonly CLOSED = 2;
  readyState = 0;
  closed = false;
  onerror: (() => void) | null = null;
  private listeners = new Map<string, ((e: { data: string }) => void)[]>();
  addEventListener(name: string, fn: (e: { data: string }) => void) {
    this.listeners.set(name, [...(this.listeners.get(name) ?? []), fn]);
  }
  close() {
    this.closed = true;
    this.readyState = 2;
  }
  emit(name: string, data: unknown) {
    for (const fn of this.listeners.get(name) ?? []) fn({ data: JSON.stringify(data) });
  }
}

function setup() {
  const sources: FakeSource[] = [];
  const subscribe = vi.fn<(c: string, s: string, t: string, a: string) => Promise<void>>(async () => {});
  const client = new EventsClient({
    createSource: () => {
      const s = new FakeSource();
      sources.push(s);
      return s;
    },
    subscribe,
    unsubscribe: vi.fn(async () => {}),
  });
  client.start();
  return { client, sources, subscribe, source: () => sources[sources.length - 1] };
}

function handlers(
  last = "",
): TurnHandlers & { messages: Message[]; ended: TurnSummary[]; errors: string[]; partials: PartialText[] } {
  const h = {
    messages: [] as Message[],
    partials: [] as PartialText[],
    onPartial: (p: PartialText) => h.partials.push(p),
    ended: [] as TurnSummary[],
    errors: [] as string[],
    onMessage: (m: Message) => h.messages.push(m),
    onStreamError: (e: string) => h.errors.push(e),
    onEnded: (t: TurnSummary) => h.ended.push(t),
    lastMessageId: () => (h.messages.length ? h.messages[h.messages.length - 1].id : last),
  };
  return h;
}

const msg = (id: string, turnId: string): Message => ({
  id,
  turnId,
  kind: "assistant",
  text: id,
  receivedAt: "2026-10-14T09:00:00.000Z",
  truncated: false,
});

const flush = () => new Promise((r) => setTimeout(r, 0));

describe("EventsClient", () => {
  it("posts subscriptions only after hello, then routes by turnId", async () => {
    const { client, source, subscribe } = setup();
    const a = handlers();
    const b = handlers();
    client.subscribe("s1", "t1", a);
    expect(subscribe).not.toHaveBeenCalled();

    source().emit("hello", { connectionId: "c1" });
    client.subscribe("s1", "t2", b);
    await flush();
    expect(subscribe.mock.calls).toEqual([
      ["c1", "s1", "t1", ""],
      ["c1", "s1", "t2", ""],
    ]);

    source().emit("message", { turnId: "t1", message: msg("m1", "t1") });
    source().emit("message", { turnId: "t2", message: msg("m2", "t2") });
    source().emit("stream.error", { turnId: "t2", message: "unreadable" });
    expect(a.messages.map((m) => m.id)).toEqual(["m1"]);
    expect(b.messages.map((m) => m.id)).toEqual(["m2"]);
    expect(b.errors).toEqual(["unreadable"]);
  });

  it("routes streamed text by turnId", async () => {
    const { client, source } = setup();
    const a = handlers();
    source().emit("hello", { connectionId: "c1" });
    client.subscribe("s1", "t1", a);
    source().emit("message.partial", { turnId: "t1", streamId: "x", text: "Hel" });
    source().emit("message.partial", { turnId: "t2", streamId: "y", text: "other" });
    expect(a.partials).toEqual([{ turnId: "t1", streamId: "x", text: "Hel" }]);
  });

  it("ends a subscription after turn.ended", async () => {
    const { client, source, subscribe } = setup();
    const a = handlers();
    source().emit("hello", { connectionId: "c1" });
    client.subscribe("s1", "t1", a);
    source().emit("turn.ended", { id: "t1", status: "completed" });
    source().emit("message", { turnId: "t1", message: msg("late", "t1") });
    expect(a.ended).toHaveLength(1);
    expect(a.messages).toHaveLength(0);

    // A reconnect does not resubscribe an ended turn.
    subscribe.mockClear();
    source().emit("hello", { connectionId: "c2" });
    await flush();
    expect(subscribe).not.toHaveBeenCalled();
  });

  it("resubscribes from the last message ID on a new hello", async () => {
    const { client, source, subscribe } = setup();
    const a = handlers();
    source().emit("hello", { connectionId: "c1" });
    client.subscribe("s1", "t1", a);
    source().emit("message", { turnId: "t1", message: msg("m1", "t1") });
    source().emit("message", { turnId: "t1", message: msg("m2", "t1") });

    const reconnected = vi.fn();
    client.onHello(reconnected);
    subscribe.mockClear();
    source().emit("hello", { connectionId: "c2" });
    await flush();
    expect(subscribe.mock.calls).toEqual([["c2", "s1", "t1", "m2"]]);
    expect(reconnected).toHaveBeenCalledOnce();
  });

  it("delivers session changes", () => {
    const { client, source } = setup();
    const changes: unknown[] = [];
    client.onSession((c) => changes.push(c));
    source().emit("session.upserted", { id: "s1", status: "idle" });
    source().emit("session.deleted", { id: "s1" });
    expect(changes).toEqual([
      { type: "upserted", session: { id: "s1", status: "idle" } },
      { type: "deleted", id: "s1" },
    ]);
  });

  it("reports a lost connection when the source closes, and reconnects", () => {
    const { client, sources, source } = setup();
    const states: string[] = [];
    client.onState((s) => states.push(s));
    source().emit("hello", { connectionId: "c1" });
    source().readyState = FakeSource.CLOSED;
    source().onerror?.();
    expect(states.at(-1)).toBe("lost");
    client.reconnect();
    expect(sources).toHaveLength(2);
    expect(sources[0].closed).toBe(true);
    source().emit("hello", { connectionId: "c2" });
    expect(states.at(-1)).toBe("open");
  });

  it("unsubscribes on the server when a live subscription is dropped", async () => {
    const unsubscribe = vi.fn(async () => {});
    const sources: FakeSource[] = [];
    const client = new EventsClient({
      createSource: () => {
        const s = new FakeSource();
        sources.push(s);
        return s;
      },
      subscribe: vi.fn(async () => {}),
      unsubscribe,
    });
    client.start();
    sources[0].emit("hello", { connectionId: "c1" });
    const stop = client.subscribe("s1", "t1", handlers());
    stop();
    expect(unsubscribe).toHaveBeenCalledWith("c1", "t1");
  });
});
