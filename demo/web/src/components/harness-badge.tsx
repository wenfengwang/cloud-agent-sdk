import type { HarnessId } from "@/lib/api/types";
import { cn } from "@/lib/utils";

export const harnessLabel: Record<HarnessId, string> = { claudecode: "Claude Code", codex: "Codex" };

/** The per-harness accent: Claude Code amber, Codex sky. */
export const harnessDot: Record<HarnessId, string> = { claudecode: "bg-amber-500", codex: "bg-sky-500" };

export function HarnessDot({ harness, className }: { harness: HarnessId; className?: string }) {
  return <span className={cn("inline-block size-1.5 shrink-0 rounded-full", harnessDot[harness], className)} />;
}

export function HarnessBadge({ harness, className }: { harness: HarnessId; className?: string }) {
  return (
    <span
      className={cn(
        "inline-flex shrink-0 items-center gap-1.5 rounded-full border px-2 py-0.5 text-xs font-medium whitespace-nowrap text-muted-foreground",
        className,
      )}
    >
      <HarnessDot harness={harness} />
      {harnessLabel[harness]}
    </span>
  );
}
