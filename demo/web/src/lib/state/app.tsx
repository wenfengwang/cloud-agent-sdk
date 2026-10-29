"use client";

import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from "react";
import { api } from "@/lib/api/client";
import { EventsClient, type ConnectionState } from "@/lib/api/events";
import type { Config, HarnessId, HarnessInfo, SessionSummary } from "@/lib/api/types";

interface AppContextValue {
  config: Config | null;
  configError: string | null;
  sessions: SessionSummary[];
  sessionById(id: string): SessionSummary | undefined;
  events: EventsClient;
  connection: ConnectionState;
  harness(id: HarnessId): HarnessInfo | undefined;
  gapReason(id: string): string;
  refreshSessions(): Promise<void>;
  removeSession(id: string): void;
  /** Hands the first prompt of a new session from home to its conversation. */
  stashPrompt(sessionId: string, prompt: string): void;
  takePrompt(sessionId: string): string | undefined;
}

const AppContext = createContext<AppContextValue | null>(null);

export function useApp(): AppContextValue {
  const ctx = useContext(AppContext);
  if (!ctx) throw new Error("useApp outside AppProvider");
  return ctx;
}

function byActivity(a: SessionSummary, b: SessionSummary): number {
  return b.lastActivityAt.localeCompare(a.lastActivityAt) || b.id.localeCompare(a.id);
}

export function AppProvider({ children }: { children: React.ReactNode }) {
  const [config, setConfig] = useState<Config | null>(null);
  const [configError, setConfigError] = useState<string | null>(null);
  const [sessionMap, setSessionMap] = useState<Map<string, SessionSummary>>(new Map());
  const [connection, setConnection] = useState<ConnectionState>("connecting");
  const prompts = useRef(new Map<string, string>());

  const [events] = useState(
    () =>
      new EventsClient({
        subscribe: api.subscribe,
        unsubscribe: api.unsubscribe,
      }),
  );

  const refreshSessions = useCallback(async () => {
    const list = await api.sessions();
    setSessionMap(new Map(list.map((s) => [s.id, s])));
  }, []);

  useEffect(() => {
    api
      .config()
      .then(setConfig)
      .catch((err) => setConfigError(String(err?.message ?? err)));
    api
      .sessions()
      .then((list) => setSessionMap(new Map(list.map((s) => [s.id, s]))))
      .catch(() => {});

    const offSession = events.onSession((change) => {
      setSessionMap((prev) => {
        const next = new Map(prev);
        if (change.type === "upserted") next.set(change.session.id, change.session);
        else next.delete(change.id);
        return next;
      });
    });
    const offHello = events.onHello(() => {
      refreshSessions().catch(() => {});
      api
        .config()
        .then(setConfig)
        .catch(() => {});
    });
    const offState = events.onState(setConnection);
    events.start();
    return () => {
      offSession();
      offHello();
      offState();
      events.close();
    };
  }, [events, refreshSessions]);

  const value = useMemo<AppContextValue>(() => {
    const gaps = new Map((config?.gaps ?? []).map((g) => [g.id, g.reason]));
    return {
      config,
      configError,
      sessions: [...sessionMap.values()].sort(byActivity),
      sessionById: (id) => sessionMap.get(id),
      events,
      connection,
      harness: (id) => config?.harnesses.find((h) => h.harness === id),
      gapReason: (id) => gaps.get(id) ?? "Not supported by the SDK.",
      refreshSessions,
      removeSession: (id) =>
        setSessionMap((prev) => {
          const next = new Map(prev);
          next.delete(id);
          return next;
        }),
      stashPrompt: (id, prompt) => prompts.current.set(id, prompt),
      takePrompt: (id) => {
        const p = prompts.current.get(id);
        prompts.current.delete(id);
        return p;
      },
    };
  }, [config, configError, sessionMap, events, connection, refreshSessions]);

  return <AppContext.Provider value={value}>{children}</AppContext.Provider>;
}

const storageKey = (name: string) => `cloudagent-demo.${name}`;

export function readPreference(name: string): string | null {
  try {
    return window.localStorage.getItem(storageKey(name));
  } catch {
    return null;
  }
}

export function writePreference(name: string, value: string): void {
  try {
    window.localStorage.setItem(storageKey(name), value);
  } catch {
    // Storage is optional.
  }
}
