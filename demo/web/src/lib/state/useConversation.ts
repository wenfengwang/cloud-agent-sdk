"use client";

import { useCallback, useEffect, useReducer, useRef, useState } from "react";
import { ApiError, api } from "@/lib/api/client";
import type { SessionSummary, TurnSummary } from "@/lib/api/types";
import { useApp } from "./app";
import { conversationReducer, initialConversation, isLocked } from "./conversation";

/**
 * One open conversation: loads the session and the history of its ended
 * turns, follows running turns through the tab's EventsClient, and owns the
 * send and stop flow.
 */
export function useConversation(sessionId: string) {
  const app = useApp();
  const { events } = app;
  const [state, dispatch] = useReducer(conversationReducer, initialConversation);
  const [loadedSummary, setLoadedSummary] = useState<SessionSummary | undefined>();
  const stateRef = useRef(state);
  useEffect(() => {
    stateRef.current = state;
  });
  const subscriptions = useRef(new Map<string, () => void>());

  const follow = useCallback(
    (turnId: string) => {
      if (subscriptions.current.has(turnId)) return;
      const stop = events.subscribe(sessionId, turnId, {
        onMessage: (message) => dispatch({ type: "message", turnId, message }),
        onStreamError: (message) => dispatch({ type: "streamError", turnId, message }),
        onEnded: (turn) => {
          subscriptions.current.delete(turnId);
          dispatch({ type: "turnEnded", turn });
        },
        lastMessageId: () => {
          const list = stateRef.current.messages[turnId] ?? [];
          return list.length ? list[list.length - 1].id : "";
        },
        onSubscribeError: () => subscriptions.current.delete(turnId),
      });
      subscriptions.current.set(turnId, stop);
    },
    [events, sessionId],
  );

  const loadHistory = useCallback(
    async (turns: TurnSummary[]) => {
      // History of ended turns, newest first, two at a time.
      const ended = turns.filter((t) => t.status !== "running" && !stateRef.current.messages[t.id]).reverse();
      const worker = async () => {
        for (let t = ended.shift(); t; t = ended.shift()) {
          try {
            const list = await api.messages(sessionId, t.id);
            dispatch({
              type: "historyLoaded",
              turnId: t.id,
              messages: list.messages,
              error: list.error?.message ?? null,
            });
          } catch {
            // A failed history fetch leaves the turn with its prompt only.
          }
        }
      };
      await Promise.all([worker(), worker()]);
    },
    [sessionId],
  );

  const load = useCallback(
    () =>
      api.session(sessionId).then(
        (detail) => {
          setLoadedSummary(detail);
          dispatch({ type: "loaded", detail });
          if (detail.activeTurnId) follow(detail.activeTurnId);
          return loadHistory(detail.turns);
        },
        (err) => {
          if (err instanceof ApiError && err.code === "not_found") dispatch({ type: "notFound" });
          else dispatch({ type: "loadFailed", message: String((err as Error).message ?? err) });
        },
      ),
    [sessionId, follow, loadHistory],
  );

  // The caller keys this hook's component by session, so state starts fresh.
  useEffect(() => {
    load();
    const offHello = events.onHello(() => load());
    // A turn started in another tab arrives as a new activeTurnId.
    const offSession = events.onSession((change) => {
      if (change.type !== "upserted" || change.session.id !== sessionId) return;
      const active = change.session.activeTurnId;
      if (!active || subscriptions.current.has(active)) return;
      if (stateRef.current.turns.some((t) => t.id === active)) follow(active);
      else load();
    });
    const subs = subscriptions.current;
    return () => {
      offHello();
      offSession();
      for (const stop of subs.values()) stop();
      subs.clear();
    };
  }, [events, load, follow, sessionId]);

  const summary = app.sessionById(sessionId) ?? loadedSummary;

  const send = useCallback(
    async (prompt: string) => {
      dispatch({ type: "sendStarted", prompt });
      let turn: TurnSummary;
      try {
        turn = await api.startTurn(sessionId, prompt);
      } catch (err) {
        const cancelled = err instanceof ApiError && err.code === "start_cancelled";
        dispatch({
          type: "sendFailed",
          prompt,
          message: cancelled ? null : String((err as Error).message ?? err),
        });
        return;
      }
      dispatch({ type: "sendSucceeded", turn });
      follow(turn.id);
    },
    [sessionId, follow],
  );

  const stop = useCallback(() => api.stop(sessionId).catch(() => {}), [sessionId]);

  // The first prompt of a session created from home.
  const { takePrompt } = app;
  useEffect(() => {
    if (state.status !== "ready" || state.turns.length > 0 || state.pending) return;
    const prompt = takePrompt(sessionId);
    if (prompt) send(prompt);
  }, [state.status, state.turns.length, state.pending, sessionId, takePrompt, send]);

  return {
    state,
    summary,
    locked: isLocked(state, summary),
    send,
    stop,
    clearRestoredPrompt: () => dispatch({ type: "promptRestored" }),
  };
}
