"use client";

import {
  ArrowUpIcon,
  ChevronDownIcon,
  CpuIcon,
  FolderIcon,
  GaugeIcon,
  GitBranchIcon,
  MicIcon,
  MonitorIcon,
  PlusIcon,
  ShieldAlertIcon,
  SquareIcon,
} from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { Command, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList } from "@/components/ui/command";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import type { HarnessId, Project } from "@/lib/api/types";
import { useApp } from "@/lib/state/app";
import { cn } from "@/lib/utils";
import { GapControl } from "./gap-control";

/** The text input card shared by home and conversations. */
export function Composer({
  value,
  onChange,
  onSubmit,
  onStop,
  running,
  locked,
  canSend,
  placeholder,
  toolbar,
  notice,
  error,
  autoFocus,
}: {
  value: string;
  onChange(value: string): void;
  onSubmit(): void;
  onStop?(): void;
  /** A turn is starting or running: the send button is Stop. */
  running: boolean;
  locked: boolean;
  canSend: boolean;
  placeholder: string;
  toolbar?: React.ReactNode;
  notice?: React.ReactNode;
  error?: string | null;
  autoFocus?: boolean;
}) {
  const { gapReason } = useApp();
  const [steeringHint, setSteeringHint] = useState(false);
  const [stopping, setStopping] = useState(false);
  const ref = useRef<HTMLTextAreaElement>(null);

  // A turn that ended clears the stop and steering states.
  const [wasRunning, setWasRunning] = useState(running);
  if (running !== wasRunning) {
    setWasRunning(running);
    if (!running) {
      setStopping(false);
      setSteeringHint(false);
    }
  }

  useEffect(() => {
    if (autoFocus) ref.current?.focus();
  }, [autoFocus]);

  const sendable = !locked && canSend && value.trim() !== "";
  const showSteering = running && (steeringHint || value.trim() !== "");

  return (
    <div className="space-y-2">
      {notice}
      {error && (
        <p className="rounded-lg border border-red-200 bg-red-50/60 px-3 py-2 text-sm break-words text-red-800">
          {error}
        </p>
      )}
      <div className="rounded-2xl border bg-background shadow-[0_1px_2px_rgba(0,0,0,0.04),0_4px_16px_rgba(0,0,0,0.04)] transition-shadow focus-within:border-foreground/20 focus-within:shadow-[0_1px_2px_rgba(0,0,0,0.05),0_6px_24px_rgba(0,0,0,0.07)]">
        <Textarea
          ref={ref}
          value={value}
          placeholder={placeholder}
          onChange={(e) => onChange(e.target.value)}
          onKeyDown={(e) => {
            if (e.key !== "Enter" || e.shiftKey || e.nativeEvent.isComposing) return;
            e.preventDefault();
            if (running) setSteeringHint(true);
            else if (sendable) onSubmit();
          }}
          className="max-h-[40vh] min-h-[52px] resize-none border-0 bg-transparent px-4 pt-3.5 pb-1 text-[15px] shadow-none focus-visible:border-0 focus-visible:ring-0 md:text-[15px]"
        />
        <div className="flex items-center gap-1 px-2 pb-2">
          <GapControl gap="attachments">
            <Button variant="ghost" size="icon-sm" aria-label="Attach files">
              <PlusIcon />
            </Button>
          </GapControl>
          <div className="flex min-w-0 flex-1 flex-wrap items-center gap-1">{toolbar}</div>
          <GapControl gap="voice">
            <Button variant="ghost" size="icon-sm" aria-label="Voice input">
              <MicIcon />
            </Button>
          </GapControl>
          {running ? (
            <Button
              size="icon-sm"
              aria-label="Stop"
              className={cn("rounded-full", stopping && "opacity-60")}
              disabled={stopping || !onStop}
              onClick={() => {
                setStopping(true);
                onStop?.();
              }}
            >
              <SquareIcon className="fill-current" />
            </Button>
          ) : (
            <Button size="icon-sm" aria-label="Send" className="rounded-full" disabled={!sendable} onClick={onSubmit}>
              <ArrowUpIcon />
            </Button>
          )}
        </div>
      </div>
      {showSteering && <p className="px-2 text-xs text-muted-foreground">Not available: {gapReason("steering")}</p>}
      {!running && value.startsWith("/") && (
        <p className="px-2 text-xs text-muted-foreground">
          Slash commands are not interpreted; this is sent as plain text.
        </p>
      )}
    </div>
  );
}

export const chip =
  "h-7 gap-1.5 rounded-lg px-2 text-[13px] font-normal text-muted-foreground hover:text-foreground [&_svg]:size-3.5";

/** The gap controls of the toolbar, labeled with what actually runs. */
export function RunModeControls({ harness }: { harness: HarnessId }) {
  return (
    <>
      <GapControl gap="permissions">
        <Button variant="ghost" className={chip}>
          <ShieldAlertIcon />
          {harness === "codex" ? "Full access" : "Bypass permissions"}
        </Button>
      </GapControl>
      <GapControl gap="effort">
        <Button variant="ghost" className={chip}>
          <GaugeIcon />
          Default effort
        </Button>
      </GapControl>
    </>
  );
}

export function LocalLabel() {
  return (
    <GapControl gap="cloud">
      <Button variant="ghost" className={chip}>
        <MonitorIcon />
        Local
      </Button>
    </GapControl>
  );
}

/** Picks a model from the configured suggestions, the default, or free text. */
export function ModelPicker({
  harness,
  value,
  onChange,
}: {
  harness: HarnessId;
  value: string;
  onChange(model: string): void;
}) {
  const app = useApp();
  const suggestions = app.harness(harness)?.modelSuggestions ?? [];
  const [open, setOpen] = useState(false);
  const [search, setSearch] = useState("");
  const pick = (m: string) => {
    onChange(m);
    setOpen(false);
    setSearch("");
  };
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button variant="ghost" className={chip}>
          <CpuIcon />
          {value || "Default model"}
          <ChevronDownIcon className="opacity-60" />
        </Button>
      </PopoverTrigger>
      <PopoverContent className="w-64 p-0" align="start">
        <Command>
          <CommandInput placeholder="Model name" value={search} onValueChange={setSearch} />
          <CommandList>
            <CommandGroup>
              <CommandItem value="__default" onSelect={() => pick("")}>
                Default model
              </CommandItem>
              {suggestions.map((m) => (
                <CommandItem key={m} value={m} onSelect={() => pick(m)}>
                  {m}
                </CommandItem>
              ))}
              {search.trim() && !suggestions.includes(search.trim()) && (
                <CommandItem value={`__custom ${search}`} onSelect={() => pick(search.trim())}>
                  Use “{search.trim()}”
                </CommandItem>
              )}
            </CommandGroup>
          </CommandList>
          <p className="border-t px-3 py-2 text-xs text-muted-foreground">{app.gapReason("model-list")}</p>
        </Command>
      </PopoverContent>
    </Popover>
  );
}

/** The model of a started session, which cannot change. */
export function ModelLabel({ model }: { model: string }) {
  return (
    <GapControl gap="model-switch">
      <Button variant="ghost" className={chip}>
        <CpuIcon />
        {model || "Default model"}
      </Button>
    </GapControl>
  );
}

/** Picks a project from GET /api/projects or an absolute path. */
export function ProjectPicker({
  projects,
  value,
  onChange,
}: {
  projects: Project[];
  value: Project | null;
  onChange(p: Project): void;
}) {
  const [open, setOpen] = useState(false);
  const [search, setSearch] = useState("");
  const typedPath = search.trim().startsWith("/") ? search.trim().replace(/\/+$/, "") || "/" : "";
  const pick = (p: Project) => {
    onChange(p);
    setOpen(false);
    setSearch("");
  };
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button variant="ghost" className={cn(chip, !value && "text-foreground")}>
          <FolderIcon />
          <span className="max-w-48 truncate">{value?.name ?? "Choose a project"}</span>
          <ChevronDownIcon className="opacity-60" />
        </Button>
      </PopoverTrigger>
      <PopoverContent className="w-80 p-0" align="start">
        <Command>
          <CommandInput placeholder="Search or enter an absolute path" value={search} onValueChange={setSearch} />
          <CommandList>
            <CommandEmpty>
              {projects.length === 0
                ? "No projects. Add a --project-root or enter an absolute path."
                : "No matching project."}
            </CommandEmpty>
            {typedPath && (
              <CommandGroup heading="Path">
                <CommandItem
                  value={`__path ${typedPath}`}
                  onSelect={() =>
                    pick({
                      path: typedPath,
                      name: typedPath.split("/").pop() || typedPath,
                      isGitRepository: false,
                      currentBranch: "",
                    })
                  }
                >
                  <FolderIcon />
                  Use {typedPath}
                </CommandItem>
              </CommandGroup>
            )}
            {projects.length > 0 && (
              <CommandGroup heading="Projects">
                {projects.map((p) => (
                  <CommandItem key={p.path} value={`${p.name} ${p.path}`} onSelect={() => pick(p)}>
                    <FolderIcon />
                    <div className="min-w-0">
                      <div className="truncate">{p.name}</div>
                      <div className="truncate text-xs text-muted-foreground">{p.path}</div>
                    </div>
                  </CommandItem>
                ))}
              </CommandGroup>
            )}
          </CommandList>
        </Command>
      </PopoverContent>
    </Popover>
  );
}

/** The worktree switch and its base branch, for git projects. */
export function WorktreeControl({
  branches,
  enabled,
  onEnabledChange,
  baseBranch,
  onBaseBranchChange,
}: {
  branches: string[];
  enabled: boolean;
  onEnabledChange(v: boolean): void;
  baseBranch: string;
  onBaseBranchChange(b: string): void;
}) {
  const [open, setOpen] = useState(false);
  return (
    <div className="flex items-center gap-1">
      <label className={cn(chip, "inline-flex cursor-pointer items-center select-none")}>
        <Switch size="sm" checked={enabled} onCheckedChange={onEnabledChange} />
        Worktree
      </label>
      <Popover open={open} onOpenChange={setOpen}>
        <PopoverTrigger asChild>
          <Button variant="ghost" className={chip} disabled={!enabled && branches.length === 0}>
            <GitBranchIcon />
            <span className="max-w-36 truncate">{baseBranch || "branch"}</span>
            {enabled && <ChevronDownIcon className="opacity-60" />}
          </Button>
        </PopoverTrigger>
        {enabled && (
          <PopoverContent className="w-60 p-0" align="start">
            <Command>
              <CommandInput placeholder="Base branch" />
              <CommandList>
                <CommandEmpty>No branch.</CommandEmpty>
                <CommandGroup heading="Start the worktree from">
                  {branches.map((b) => (
                    <CommandItem
                      key={b}
                      value={b}
                      onSelect={() => {
                        onBaseBranchChange(b);
                        setOpen(false);
                      }}
                    >
                      <GitBranchIcon />
                      {b}
                    </CommandItem>
                  ))}
                </CommandGroup>
              </CommandList>
            </Command>
          </PopoverContent>
        )}
      </Popover>
    </div>
  );
}
