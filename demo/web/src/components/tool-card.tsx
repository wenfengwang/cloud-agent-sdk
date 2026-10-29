"use client";

import {
  BotIcon,
  CheckIcon,
  ChevronRightIcon,
  FileDiffIcon,
  FilePenIcon,
  FilePlusIcon,
  FileTextIcon,
  GlobeIcon,
  Loader2Icon,
  PlugIcon,
  SearchIcon,
  TerminalIcon,
  WrenchIcon,
  XIcon,
} from "lucide-react";
import { useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import type { ToolInput, ToolOutput, ToolName } from "@/lib/api/types";
import type { Item, ToolItem } from "@/lib/state/model";
import { cn } from "@/lib/utils";
import { CopyButton } from "./copy-button";
import { DiffStat, DiffView, countLines, lineDiff, parseUnified } from "./diff-view";
import { useRelativePath } from "./work-dir";

const icons: Record<ToolName, React.ComponentType<{ className?: string }>> = {
  shell: TerminalIcon,
  file_read: FileTextIcon,
  file_write: FilePlusIcon,
  file_edit: FilePenIcon,
  file_patch: FileDiffIcon,
  glob: SearchIcon,
  grep: SearchIcon,
  web_fetch: GlobeIcon,
  web_search: GlobeIcon,
  mcp: PlugIcon,
  subagent: BotIcon,
  unknown: WrenchIcon,
};

const labels: Record<ToolName, string> = {
  shell: "Ran",
  file_read: "Read",
  file_write: "Wrote",
  file_edit: "Edited",
  file_patch: "Edited",
  glob: "Searched",
  grep: "Searched",
  web_fetch: "Fetched",
  web_search: "Searched the web",
  mcp: "Called",
  subagent: "Delegated",
  unknown: "",
};

function hostOf(url: string): string {
  try {
    return new URL(url).host;
  } catch {
    return url;
  }
}

function json(v: unknown): string {
  if (typeof v === "string") return v;
  try {
    return JSON.stringify(v, null, 2) ?? "null";
  } catch {
    return String(v);
  }
}

/** The one-line target of a call, and a diff stat when the tool edits. */
function summarize(
  input: ToolInput | undefined,
  rel: (path: string) => string,
): {
  target: string;
  stat?: { additions: number; deletions: number };
  badge?: string;
} {
  if (!input) return { target: "" };
  switch (input.type) {
    case "shell":
      return { target: `$ ${input.command}` };
    case "fileRead": {
      const range =
        input.offset || input.limit
          ? ` · lines ${input.offset + 1}-${input.limit ? input.offset + input.limit : "end"}`
          : "";
      return { target: rel(input.path) + range };
    }
    case "fileWrite":
      return { target: rel(input.path), stat: { additions: input.content.split("\n").length, deletions: 0 } };
    case "fileEdit":
      return {
        target: rel(input.path),
        stat: countLines(lineDiff(input.oldString, input.newString)),
        badge: input.replaceAll ? "all occurrences" : undefined,
      };
    case "filePatch": {
      const first = rel(input.changes[0]?.path ?? "");
      const more = input.changes.length > 1 ? ` +${input.changes.length - 1} more` : "";
      const totals = input.changes.reduce(
        (acc, c) => {
          const n = countLines(parseUnified(c.diff));
          return { additions: acc.additions + n.additions, deletions: acc.deletions + n.deletions };
        },
        { additions: 0, deletions: 0 },
      );
      return { target: first + more, stat: totals };
    }
    case "glob":
      return { target: [input.pattern, input.path && rel(input.path)].filter(Boolean).join(" in ") };
    case "grep":
      return { target: [input.pattern, input.glob, input.path && rel(input.path)].filter(Boolean).join(" · ") };
    case "webFetch":
      return { target: hostOf(input.url) };
    case "webSearch":
      return { target: input.query };
    case "mcp":
      return { target: `${input.server} · ${input.tool}` };
    case "subagent":
      return { target: input.description, badge: input.subagentType || undefined };
    case "unknown":
      return { target: "" };
  }
}

function Section({ title, text, children }: { title: string; text?: string; children?: React.ReactNode }) {
  return (
    <div className="space-y-1">
      <div className="flex items-center justify-between">
        <span className="text-[11px] font-medium tracking-wide text-muted-foreground uppercase">{title}</span>
        {text !== undefined && <CopyButton text={text} />}
      </div>
      {children ?? (
        <pre className="max-h-80 overflow-auto rounded-md bg-muted/60 px-3 py-2 font-mono text-xs leading-5 whitespace-pre-wrap break-words">
          {text || " "}
        </pre>
      )}
    </div>
  );
}

function outputText(output: ToolOutput | undefined): string | undefined {
  if (!output) return undefined;
  switch (output.type) {
    case "shell":
      return output.output;
    case "fileRead":
      return output.content;
    case "text":
      return output.text;
    case "mcp":
      return mcpText(output.content);
    case "unknown":
      return json(output.raw);
  }
}

/** MCP text content blocks render as text; anything else as JSON. */
function mcpText(content: unknown): string {
  if (
    Array.isArray(content) &&
    content.every((b) => b && typeof b === "object" && (b as { type?: string }).type === "text")
  ) {
    return content.map((b) => (b as { text?: string }).text ?? "").join("\n");
  }
  return json(content);
}

function InputBody({ input }: { input: ToolInput }) {
  switch (input.type) {
    case "shell":
      return <Section title="Command" text={input.command} />;
    case "fileWrite":
      return (
        <Section title={input.path}>
          <DiffView lines={lineDiff("", input.content)} className="max-h-80 rounded-md border" />
        </Section>
      );
    case "fileEdit":
      return (
        <Section title={input.path}>
          <DiffView lines={lineDiff(input.oldString, input.newString)} className="max-h-80 rounded-md border" />
        </Section>
      );
    case "filePatch":
      return (
        <div className="space-y-3">
          {input.changes.map((c, i) => (
            <Section key={i} title={`${c.kind} · ${c.path}`}>
              <DiffView lines={parseUnified(c.diff)} className="max-h-80 rounded-md border" />
            </Section>
          ))}
        </div>
      );
    case "webFetch":
      return (
        <div className="space-y-2">
          <a
            href={input.url}
            target="_blank"
            rel="noopener noreferrer"
            className="text-xs break-all text-sky-700 hover:underline"
          >
            {input.url}
          </a>
          {input.prompt && <Section title="Prompt" text={input.prompt} />}
        </div>
      );
    case "mcp":
      return <Section title="Arguments" text={json(input.arguments)} />;
    case "subagent":
      return input.prompt ? <Section title="Prompt" text={input.prompt} /> : null;
    case "unknown":
      return <Section title="Input" text={json(input.raw)} />;
    default:
      return null;
  }
}

function StatusMark({ item }: { item: ToolItem }) {
  switch (item.state) {
    case "running":
      return <Loader2Icon className="size-3.5 animate-spin text-muted-foreground" />;
    case "succeeded":
      return <CheckIcon className="size-3.5 text-emerald-600" />;
    case "failed":
      return <XIcon className="size-3.5 text-red-600" />;
    case "interrupted":
      return <span className="text-xs text-muted-foreground">Interrupted</span>;
  }
}

export function ToolCard({
  item,
  renderChildren,
}: {
  item: ToolItem;
  renderChildren: (items: Item[]) => React.ReactNode;
}) {
  const tool = item.call?.tool ?? item.result?.tool ?? "unknown";
  const isSubagent = tool === "subagent";
  const [open, setOpen] = useState<boolean | null>(null);
  const expanded = open ?? (isSubagent && item.state === "running");
  const Icon = icons[tool];
  const label = labels[tool] || item.call?.rawName || "Tool";
  const rel = useRelativePath();
  const { target, stat, badge } = summarize(item.call?.input, rel);
  const output = item.result?.output;
  const text = outputText(output);
  const exitCode = output?.type === "shell" ? output.exitCode : null;

  return (
    <Collapsible open={expanded} onOpenChange={setOpen} className="group/tool">
      <CollapsibleTrigger className="flex w-full min-w-0 items-center gap-2 rounded-md px-2 py-1 text-left text-sm transition-colors hover:bg-muted/70">
        <ChevronRightIcon
          className={cn("size-3.5 shrink-0 text-muted-foreground transition-transform", expanded && "rotate-90")}
        />
        <Icon className="size-3.5 shrink-0 text-muted-foreground" />
        <span className="shrink-0 font-medium">{label}</span>
        <span
          className="min-w-0 truncate font-mono text-xs text-muted-foreground"
          title={item.call?.input.type === "shell" ? item.call.input.description : undefined}
        >
          {target}
        </span>
        {badge && (
          <Badge variant="secondary" className="shrink-0 text-[10px]">
            {badge}
          </Badge>
        )}
        {stat && <DiffStat {...stat} className="shrink-0" />}
        {exitCode !== null && exitCode !== 0 && (
          <Badge variant="outline" className="shrink-0 font-mono text-[10px]">
            exit {exitCode}
          </Badge>
        )}
        {item.truncated && (
          <Badge variant="outline" className="shrink-0 text-[10px]">
            Truncated
          </Badge>
        )}
        <span className="ml-auto flex shrink-0 items-center pl-2">
          <StatusMark item={item} />
        </span>
      </CollapsibleTrigger>
      <CollapsibleContent>
        <div className="mt-1 mb-2 ml-[1.1rem] space-y-3 border-l pl-4">
          {item.call && <InputBody input={item.call.input} />}
          {isSubagent && item.children.length > 0 && <div className="space-y-2">{renderChildren(item.children)}</div>}
          {text !== undefined && (output?.type !== "text" || text) && (
            <Section title={item.result?.isError ? "Error" : isSubagent ? "Result" : "Output"} text={text} />
          )}
          {!item.result && item.state !== "running" && (
            <p className="text-xs text-muted-foreground">No result was reported.</p>
          )}
        </div>
      </CollapsibleContent>
    </Collapsible>
  );
}
