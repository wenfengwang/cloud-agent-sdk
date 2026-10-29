"use client";

import {
  AlertCircleIcon,
  BrainIcon,
  ChevronRightIcon,
  Loader2Icon,
  RotateCcwIcon,
  TriangleAlertIcon,
} from "lucide-react";
import { useEffect, useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import type { Message, PartialText, TurnSummary } from "@/lib/api/types";
import { between, formatDuration, formatUsage } from "@/lib/state/format";
import { buildItems, type Item, type TextItem } from "@/lib/state/model";
import { cn } from "@/lib/utils";
import { Markdown } from "./markdown";
import { ToolCard } from "./tool-card";

export function UserBubble({ text, pending }: { text: string; pending?: boolean }) {
  return (
    <div className="flex justify-end">
      <div
        className={cn(
          "max-w-[80%] rounded-2xl bg-muted px-4 py-2.5 text-[15px] leading-relaxed whitespace-pre-wrap break-words",
          pending && "opacity-70",
        )}
      >
        {text}
      </div>
    </div>
  );
}

function Reasoning({ item }: { item: TextItem }) {
  const empty = item.text.trim() === "";
  return (
    <Collapsible>
      <CollapsibleTrigger
        disabled={empty}
        className="group/r flex items-center gap-1.5 rounded-md px-2 py-1 text-sm text-muted-foreground hover:bg-muted/70 disabled:hover:bg-transparent"
      >
        {!empty && <ChevronRightIcon className="size-3.5 transition-transform group-data-[state=open]/r:rotate-90" />}
        <BrainIcon className="size-3.5" />
        Thought
      </CollapsibleTrigger>
      {!empty && (
        <CollapsibleContent>
          <div className="mt-1 ml-[1.1rem] border-l pl-4 text-sm whitespace-pre-wrap text-muted-foreground">
            {item.text}
          </div>
        </CollapsibleContent>
      )}
    </Collapsible>
  );
}

function ItemView({ item, onRetry }: { item: Item; onRetry?: (prompt: string) => void }) {
  switch (item.kind) {
    case "user":
      return <UserBubble text={item.text} pending={item.optimistic} />;
    case "assistant":
      return (
        <div className="space-y-1">
          <Markdown text={item.text} />
          {item.truncated && <Badge variant="outline">Truncated</Badge>}
        </div>
      );
    case "reasoning":
      return <Reasoning item={item} />;
    case "tool":
      return <ToolCard item={item} renderChildren={(children) => <Items items={children} />} />;
    case "turnEnd":
      return <TurnEnd turn={item.turn} onRetry={onRetry} />;
  }
}

function Items({ items, onRetry }: { items: Item[]; onRetry?: (prompt: string) => void }) {
  // Consecutive tool and reasoning rows sit closer together than prose.
  return (
    <>
      {items.map((item, i) => {
        const compactRow = item.kind === "tool" || item.kind === "reasoning";
        const prevCompact = i > 0 && (items[i - 1].kind === "tool" || items[i - 1].kind === "reasoning");
        return (
          <div key={item.id} className={cn(i > 0 && (compactRow && prevCompact ? "mt-0.5" : "mt-4"))}>
            <ItemView item={item} onRetry={onRetry} />
          </div>
        );
      })}
    </>
  );
}

function useNow(active: boolean): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!active) return;
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, [active]);
  return now;
}

/** Shown while a turn starts or runs, timed from when the prompt was sent. */
export function WorkingIndicator({ since }: { since?: number }) {
  const now = useNow(true);
  return (
    <div className="flex items-center gap-2 px-2 text-sm text-muted-foreground">
      <Loader2Icon className="size-3.5 animate-spin" />
      <span className="shimmer">Working</span>
      {since !== undefined && <span className="tabular-nums">· {formatDuration(now - since)}</span>}
    </div>
  );
}

/** Assistant text still being generated, with a caret. */
function StreamingText({ text }: { text: string }) {
  return (
    <div className="streaming">
      <Markdown text={text} />
    </div>
  );
}

function TurnEnd({ turn, onRetry }: { turn: TurnSummary; onRetry?: (prompt: string) => void }) {
  const duration = formatDuration(between(turn.startedAt, turn.endedAt));
  if (turn.status === "failed") {
    return (
      <div className="flex items-start gap-3 rounded-lg border border-red-200 bg-red-50/60 px-3 py-2.5 text-sm">
        <AlertCircleIcon className="mt-0.5 size-4 shrink-0 text-red-600" />
        <div className="min-w-0 flex-1">
          <p className="font-medium text-red-900">The turn failed</p>
          <p className="mt-0.5 break-words text-red-800/90">{turn.error?.message}</p>
        </div>
        {onRetry && (
          <Button size="sm" variant="outline" onClick={() => onRetry(turn.prompt)}>
            <RotateCcwIcon /> Retry
          </Button>
        )}
      </div>
    );
  }
  const label =
    turn.status === "completed"
      ? `Worked for ${duration}`
      : turn.status === "closed"
        ? "Session closed"
        : `Stopped after ${duration}`;
  const usage = formatUsage(turn.usage);
  return (
    <div className="flex items-center gap-3 text-xs text-muted-foreground">
      <div className="h-px flex-1 bg-border" />
      {usage ? (
        <Tooltip>
          <TooltipTrigger className="cursor-default rounded px-1 outline-none focus-visible:ring-2 focus-visible:ring-ring/50">
            {label}
          </TooltipTrigger>
          <TooltipContent>
            <span className="text-muted-foreground/80">Demo diagnostics · </span>
            {usage}
          </TooltipContent>
        </Tooltip>
      ) : (
        <span>{label}</span>
      )}
      <div className="h-px flex-1 bg-border" />
    </div>
  );
}

export function Transcript({
  turns,
  messages,
  streamErrors,
  partials,
  sentAt,
  pending,
  onRetry,
}: {
  turns: TurnSummary[];
  messages: Record<string, Message[]>;
  streamErrors: Record<string, string>;
  partials: Record<string, PartialText[]>;
  sentAt: Record<string, number>;
  pending: { prompt: string; sentAt?: number } | null;
  onRetry?: (prompt: string) => void;
}) {
  return (
    <div className="space-y-6">
      {turns.map((turn) => {
        const items = buildItems(turn, messages[turn.id] ?? []);
        const running = turn.status === "running";
        return (
          <section key={turn.id} className="space-y-0">
            <Items items={running ? items : items.slice(0, -1)} onRetry={onRetry} />
            {streamErrors[turn.id] && (
              <div className="mt-4 flex items-start gap-2 rounded-lg border border-amber-200 bg-amber-50/60 px-3 py-2 text-sm text-amber-900">
                <TriangleAlertIcon className="mt-0.5 size-4 shrink-0" />
                Some messages of this turn could not be read: {streamErrors[turn.id]}
              </div>
            )}
            {running &&
              (partials[turn.id] ?? [])
                .filter((p) => !p.parentToolUseId)
                .map((p) => (
                  <div key={p.streamId} className="mt-4">
                    <StreamingText text={p.text} />
                  </div>
                ))}
            <div className="mt-4">
              {running ? (
                <WorkingIndicator since={sentAt[turn.id] ?? Date.parse(turn.startedAt)} />
              ) : (
                <ItemView item={items[items.length - 1]} onRetry={onRetry} />
              )}
            </div>
          </section>
        );
      })}
      {pending && (
        <section className="space-y-4">
          <UserBubble text={pending.prompt} pending />
          <WorkingIndicator since={pending.sentAt} />
        </section>
      )}
    </div>
  );
}
