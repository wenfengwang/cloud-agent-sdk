"use client";

import {
  HistoryIcon,
  Loader2Icon,
  MoreHorizontalIcon,
  PlusIcon,
  SearchIcon,
  SettingsIcon,
  Trash2Icon,
} from "lucide-react";
import Link from "next/link";
import { usePathname, useSearchParams } from "next/navigation";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import type { SessionSummary } from "@/lib/api/types";
import { useApp } from "@/lib/state/app";
import { initials } from "@/lib/state/format";
import { cn } from "@/lib/utils";
import { useDeleteSession } from "./delete-session";
import { GapControl } from "./gap-control";
import { HarnessDot, harnessLabel } from "./harness-badge";

function groupByProject(sessions: SessionSummary[]) {
  const groups = new Map<string, { name: string; path: string; sessions: SessionSummary[] }>();
  for (const s of sessions) {
    const g = groups.get(s.projectPath) ?? { name: s.projectName, path: s.projectPath, sessions: [] };
    g.sessions.push(s);
    groups.set(s.projectPath, g);
  }
  return [...groups.values()]; // sessions arrive newest first, so groups are too
}

function SessionRow({ session, active }: { session: SessionSummary; active: boolean }) {
  const askDelete = useDeleteSession();
  const busy = session.status === "starting" || session.status === "running";
  const closing = session.status === "closing";
  return (
    <div
      className={cn(
        "group/row relative flex items-center rounded-md text-sm transition-colors hover:bg-sidebar-accent",
        active && "bg-sidebar-accent font-medium",
        closing && "opacity-50",
      )}
    >
      <Link
        href={`/?session=${encodeURIComponent(session.id)}`}
        className="flex min-w-0 flex-1 items-center gap-2.5 py-1.5 pr-8 pl-2.5"
      >
        <Tooltip>
          <TooltipTrigger asChild>
            <span className="flex size-3.5 shrink-0 items-center justify-center">
              {busy ? (
                <Loader2Icon className="size-3.5 animate-spin text-muted-foreground" />
              ) : session.lastTurnStatus === "failed" ? (
                <span className="size-1.5 rounded-full bg-red-500" />
              ) : (
                <HarnessDot harness={session.harness} />
              )}
            </span>
          </TooltipTrigger>
          <TooltipContent side="right">
            {harnessLabel[session.harness]} ·{" "}
            {closing ? "closing" : busy ? "working" : session.lastTurnStatus === "failed" ? "last turn failed" : "idle"}
          </TooltipContent>
        </Tooltip>
        <span className={cn("truncate", !session.title && "text-muted-foreground")}>
          {session.title || "New session"}
        </span>
        {closing && <span className="ml-auto text-xs text-muted-foreground">Closing</span>}
      </Link>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button
            variant="ghost"
            size="icon-xs"
            aria-label="Session actions"
            disabled={closing}
            className="absolute right-1 opacity-0 group-hover/row:opacity-100 focus-visible:opacity-100 aria-expanded:opacity-100"
          >
            <MoreHorizontalIcon />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="start" side="right">
          <DropdownMenuItem variant="destructive" onSelect={() => askDelete(session)}>
            <Trash2Icon /> Delete session
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  );
}

export function Sidebar() {
  const app = useApp();
  const params = useSearchParams();
  const pathname = usePathname();
  const current = params.get("session");
  const groups = groupByProject(app.sessions);
  const name = app.config?.displayName ?? "";

  return (
    <aside className="flex h-full w-[272px] shrink-0 flex-col border-r bg-sidebar">
      <div className="flex h-12 items-center gap-2 px-4">
        <div className="flex size-6 items-center justify-center rounded-md bg-foreground text-background">
          <span className="text-[11px] font-bold">ca</span>
        </div>
        <span className="text-sm font-semibold tracking-tight">cloudagent</span>
        <span className="rounded border px-1 text-[10px] text-muted-foreground">demo</span>
      </div>

      <div className="space-y-2 px-3 pb-3">
        <Button asChild variant="outline" className="w-full justify-start gap-2 bg-background">
          <Link href="/">
            <PlusIcon /> New session
          </Link>
        </Button>
        <GapControl gap="search" className="block">
          <div className="flex h-8 items-center gap-2 rounded-lg border bg-background px-2.5 text-sm text-muted-foreground">
            <SearchIcon className="size-3.5" />
            Search
          </div>
        </GapControl>
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto">
        <nav className="space-y-5 px-3 pb-4">
          {groups.length === 0 && <p className="px-2.5 pt-2 text-sm text-muted-foreground">No sessions yet.</p>}
          {groups.map((g) => (
            <div key={g.path}>
              <div className="group/head flex items-center justify-between px-2.5 pb-1">
                <span className="truncate text-xs font-medium text-muted-foreground" title={g.path}>
                  {g.name}
                </span>
                <Tooltip>
                  <TooltipTrigger asChild>
                    <Button
                      asChild
                      variant="ghost"
                      size="icon-xs"
                      className="opacity-0 group-hover/head:opacity-100"
                      aria-label={`New session in ${g.name}`}
                    >
                      <Link href={`/?project=${encodeURIComponent(g.path)}`}>
                        <PlusIcon />
                      </Link>
                    </Button>
                  </TooltipTrigger>
                  <TooltipContent side="right">New session in {g.name}</TooltipContent>
                </Tooltip>
              </div>
              <div className="space-y-px">
                {g.sessions.map((s) => (
                  <SessionRow key={s.id} session={s} active={pathname === "/" && current === s.id} />
                ))}
              </div>
            </div>
          ))}
          <GapControl gap="history" className="flex">
            <p className="flex items-start gap-1.5 px-2.5 text-xs text-muted-foreground">
              <HistoryIcon className="mt-px size-3.5 shrink-0" />
              Sessions from earlier backend runs are not listed
            </p>
          </GapControl>
        </nav>
      </div>

      <div className="flex items-center gap-2.5 border-t px-4 py-3">
        <div className="flex size-7 items-center justify-center rounded-full bg-muted text-[11px] font-medium">
          {initials(name || "?")}
        </div>
        <div className="min-w-0 flex-1">
          <div className="truncate text-sm font-medium">{name || "…"}</div>
          <div className="text-xs text-muted-foreground">Local backend</div>
        </div>
        <Button
          asChild
          variant="ghost"
          size="icon-sm"
          aria-label="Settings"
          className={cn(pathname.startsWith("/settings") && "bg-sidebar-accent")}
        >
          <Link href="/settings">
            <SettingsIcon />
          </Link>
        </Button>
      </div>
    </aside>
  );
}
