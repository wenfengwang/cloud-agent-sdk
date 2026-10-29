"use client";

import { FolderIcon, GitBranchIcon, MoreHorizontalIcon, SearchXIcon, Trash2Icon } from "lucide-react";
import { useLayoutEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Skeleton } from "@/components/ui/skeleton";
import { useApp } from "@/lib/state/app";
import { useConversation } from "@/lib/state/useConversation";
import { ChangesPanel, totals, useChanges } from "./changes-panel";
import { Composer, ModelLabel } from "./composer";
import { useDeleteSession } from "./delete-session";
import { DiffStat } from "./diff-view";
import { HarnessBadge } from "./harness-badge";
import { HarnessNotice } from "./harness-notice";
import { Transcript } from "./transcript";
import { WorkDirContext } from "./work-dir";

export function Conversation({ sessionId }: { sessionId: string }) {
  const app = useApp();
  const { state, summary, locked, send, stop, clearRestoredPrompt } = useConversation(sessionId);
  const [prompt, setPrompt] = useState("");
  const [showChanges, setShowChanges] = useState(false);
  const askDelete = useDeleteSession();

  // A failed start puts the prompt back.
  if (state.restoredPrompt !== null) {
    setPrompt(state.restoredPrompt);
    clearRestoredPrompt();
  }

  const endedKey = state.turns
    .filter((t) => t.status !== "running")
    .map((t) => t.id)
    .join(",");
  const changes = useChanges(sessionId, endedKey);

  // Keep the transcript pinned to the bottom unless the user scrolled up.
  const scroller = useRef<HTMLDivElement>(null);
  const pinned = useRef(true);
  useLayoutEffect(() => {
    const el = scroller.current;
    if (el && pinned.current) el.scrollTop = el.scrollHeight;
  });

  if (state.status === "notFound") {
    return (
      <div className="flex h-full flex-col items-center justify-center gap-3 px-6 text-center">
        <SearchXIcon className="size-8 text-muted-foreground" />
        <h2 className="text-lg font-medium">Session not found</h2>
        <p className="max-w-sm text-sm text-muted-foreground">
          {app.gapReason("history")} A restarted backend forgets its sessions.
        </p>
      </div>
    );
  }

  const harness = summary?.harness ?? "claudecode";
  const running = summary?.status === "starting" || summary?.status === "running" || !!state.pending;
  const closing = summary?.status === "closing";
  const harnessAvailable = app.harness(harness)?.available !== false;

  const submit = () => {
    const text = prompt.trim();
    if (!text) return;
    setPrompt("");
    pinned.current = true;
    send(text);
  };

  const changeTotals = changes.state.status === "ready" ? totals(changes.state.changes) : null;

  return (
    <div className="flex h-full min-h-0">
      <div className="flex min-w-0 flex-1 flex-col">
        <header className="flex h-12 shrink-0 items-center gap-3 border-b px-4">
          <h1 className="min-w-0 truncate text-sm font-medium">{summary?.title || "New session"}</h1>
          {summary && <HarnessBadge harness={summary.harness} />}
          {summary && (
            <span className="hidden min-w-0 items-center gap-1 truncate text-xs text-muted-foreground lg:flex">
              <FolderIcon className="size-3.5 shrink-0" />
              <span className="truncate">{summary.projectName}</span>
              {summary.worktree && (
                <>
                  <GitBranchIcon className="ml-1.5 size-3.5 shrink-0" />
                  <span className="truncate font-mono">{summary.worktree.branch}</span>
                </>
              )}
            </span>
          )}
          <div className="ml-auto flex items-center gap-1">
            {changeTotals && changes.state.status === "ready" && changes.state.changes.files.length > 0 && (
              <Button variant={showChanges ? "secondary" : "ghost"} size="sm" onClick={() => setShowChanges((v) => !v)}>
                <DiffStat {...changeTotals} />
              </Button>
            )}
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button variant="ghost" size="icon-sm" aria-label="Session menu" disabled={!summary || closing}>
                  <MoreHorizontalIcon />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end">
                <DropdownMenuItem variant="destructive" onSelect={() => summary && askDelete(summary)}>
                  <Trash2Icon /> Delete session
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </div>
        </header>

        <div
          ref={scroller}
          className="min-h-0 flex-1 overflow-y-auto"
          onScroll={(e) => {
            const el = e.currentTarget;
            pinned.current = el.scrollHeight - el.scrollTop - el.clientHeight < 80;
          }}
        >
          <div className="mx-auto w-full max-w-3xl px-6 py-8">
            {state.status === "loading" ? (
              <div className="space-y-4">
                <Skeleton className="ml-auto h-10 w-1/2 rounded-2xl" />
                <Skeleton className="h-4 w-3/4" />
                <Skeleton className="h-4 w-2/3" />
              </div>
            ) : state.status === "error" ? (
              <p className="text-sm text-red-700">{state.error}</p>
            ) : (
              <WorkDirContext value={summary?.workDir ?? ""}>
                <Transcript
                  turns={state.turns}
                  messages={state.messages}
                  streamErrors={state.streamErrors}
                  pending={state.pending}
                  harness={harness}
                  onRetry={locked ? undefined : (p) => send(p)}
                />
              </WorkDirContext>
            )}
          </div>
        </div>

        <div className="mx-auto w-full max-w-3xl shrink-0 px-6 pb-5">
          <Composer
            value={prompt}
            onChange={setPrompt}
            onSubmit={submit}
            onStop={stop}
            running={running && !closing}
            locked={locked || closing}
            canSend={harnessAvailable && state.status === "ready"}
            placeholder={closing ? "This session is closing" : "Reply…"}
            error={state.sendError}
            notice={<HarnessNotice harness={harness} />}
            autoFocus
            toolbar={summary && <ModelLabel model={summary.model} />}
          />
        </div>
      </div>
      {showChanges && (
        <ChangesPanel
          state={changes.state}
          onReload={changes.reload}
          onClose={() => setShowChanges(false)}
          hasWorktree={!!summary?.worktree}
        />
      )}
    </div>
  );
}
