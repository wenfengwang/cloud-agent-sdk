"use client";

import { FileIcon, RefreshCwIcon, XIcon } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { ScrollArea } from "@/components/ui/scroll-area";
import { ApiError, api } from "@/lib/api/client";
import type { Changes } from "@/lib/api/types";
import { cn } from "@/lib/utils";
import { DiffStat, DiffView, parseUnified } from "./diff-view";

export type ChangesState =
  | { status: "loading" }
  | { status: "ready"; changes: Changes }
  | { status: "unavailable"; message: string }
  | { status: "error"; message: string };

/** The session's changes since its start commit, refetched when refreshKey changes. */
export function useChanges(sessionId: string, refreshKey: string) {
  const [state, setState] = useState<ChangesState>({ status: "loading" });
  const load = useCallback(() => {
    api
      .changes(sessionId)
      .then((changes) => setState({ status: "ready", changes }))
      .catch((err) => {
        if (err instanceof ApiError && err.code === "invalid_request") {
          setState({ status: "unavailable", message: "Changes need a git repository." });
        } else setState({ status: "error", message: String(err.message ?? err) });
      });
  }, [sessionId]);
  useEffect(() => {
    load();
  }, [load, refreshKey]);
  return { state, reload: load };
}

export function totals(changes: Changes) {
  return changes.files.reduce(
    (acc, f) => ({ additions: acc.additions + f.additions, deletions: acc.deletions + f.deletions }),
    { additions: 0, deletions: 0 },
  );
}

const statusMark: Record<string, { letter: string; className: string }> = {
  added: { letter: "A", className: "text-emerald-600" },
  modified: { letter: "M", className: "text-amber-600" },
  deleted: { letter: "D", className: "text-red-600" },
  renamed: { letter: "R", className: "text-sky-600" },
};

export function ChangesPanel({
  state,
  onReload,
  onClose,
  hasWorktree,
}: {
  state: ChangesState;
  onReload(): void;
  onClose(): void;
  hasWorktree: boolean;
}) {
  const [selected, setSelected] = useState<string | null>(null);
  const files = state.status === "ready" ? state.changes.files : [];
  const current = files.find((f) => f.path === selected) ?? files[0];

  return (
    <aside className="flex h-full w-[42%] min-w-80 flex-col border-l bg-background">
      <div className="flex h-12 shrink-0 items-center gap-2 border-b px-4">
        <h2 className="text-sm font-medium">Changes since session start</h2>
        {state.status === "ready" && <DiffStat {...totals(state.changes)} />}
        <div className="ml-auto flex items-center">
          <Button variant="ghost" size="icon-sm" aria-label="Refresh changes" onClick={onReload}>
            <RefreshCwIcon />
          </Button>
          <Button variant="ghost" size="icon-sm" aria-label="Close changes" onClick={onClose}>
            <XIcon />
          </Button>
        </div>
      </div>
      {!hasWorktree && state.status === "ready" && (
        <p className="border-b bg-muted/40 px-4 py-2 text-xs text-muted-foreground">
          Includes manual edits made in this directory.
        </p>
      )}
      {state.status === "loading" && <p className="p-4 text-sm text-muted-foreground">Loading changes…</p>}
      {(state.status === "unavailable" || state.status === "error") && (
        <p className="p-4 text-sm text-muted-foreground">{state.message}</p>
      )}
      {state.status === "ready" && files.length === 0 && (
        <p className="p-4 text-sm text-muted-foreground">No changes yet.</p>
      )}
      {state.status === "ready" && files.length > 0 && (
        <div className="flex min-h-0 flex-1 flex-col">
          <ScrollArea className="max-h-[35%] shrink-0 border-b">
            <ul className="py-1">
              {files.map((f) => (
                <li key={f.path}>
                  <button
                    className={cn(
                      "flex w-full items-center gap-2 px-4 py-1.5 text-left text-sm hover:bg-muted/70",
                      current?.path === f.path && "bg-muted",
                    )}
                    onClick={() => setSelected(f.path)}
                  >
                    <span className={cn("w-3 font-mono text-xs font-semibold", statusMark[f.status]?.className)}>
                      {statusMark[f.status]?.letter}
                    </span>
                    <FileIcon className="size-3.5 shrink-0 text-muted-foreground" />
                    <span className="min-w-0 flex-1 truncate font-mono text-xs">{f.path}</span>
                    <DiffStat additions={f.additions} deletions={f.deletions} />
                  </button>
                </li>
              ))}
            </ul>
          </ScrollArea>
          {current && <DiffView lines={parseUnified(current.diff)} className="min-h-0 flex-1 py-2" />}
        </div>
      )}
    </aside>
  );
}
