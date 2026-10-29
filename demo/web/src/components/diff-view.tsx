import { diffLines } from "diff";
import { cn } from "@/lib/utils";

type Line = { kind: "add" | "remove" | "context" | "hunk" | "meta"; text: string };

/** Parses a unified diff into display lines. */
export function parseUnified(diff: string): Line[] {
  const lines: Line[] = [];
  for (const text of diff.replace(/\n$/, "").split("\n")) {
    if (text.startsWith("@@")) lines.push({ kind: "hunk", text });
    else if (
      /^(diff --git|index |--- |\+\+\+ |new file mode|deleted file mode|similarity index|rename (from|to)|old mode|new mode|\\ No newline)/.test(
        text,
      )
    )
      lines.push({ kind: "meta", text });
    else if (text.startsWith("+")) lines.push({ kind: "add", text: text.slice(1) });
    else if (text.startsWith("-")) lines.push({ kind: "remove", text: text.slice(1) });
    else lines.push({ kind: "context", text: text.startsWith(" ") ? text.slice(1) : text });
  }
  return lines;
}

/** Diffs two strings line by line. */
export function lineDiff(before: string, after: string): Line[] {
  const lines: Line[] = [];
  for (const part of diffLines(before, after)) {
    const kind = part.added ? "add" : part.removed ? "remove" : "context";
    for (const text of part.value.replace(/\n$/, "").split("\n")) lines.push({ kind, text });
  }
  return lines;
}

export function countLines(lines: Line[]): { additions: number; deletions: number } {
  return {
    additions: lines.filter((l) => l.kind === "add").length,
    deletions: lines.filter((l) => l.kind === "remove").length,
  };
}

const styles: Record<Line["kind"], string> = {
  add: "bg-emerald-50 text-emerald-950",
  remove: "bg-red-50 text-red-950",
  context: "text-foreground/80",
  hunk: "bg-muted/70 text-muted-foreground",
  meta: "text-muted-foreground",
};
const marks: Record<Line["kind"], string> = { add: "+", remove: "−", context: " ", hunk: "", meta: "" };

export function DiffView({
  lines,
  className,
  hideMeta = true,
}: {
  lines: Line[];
  className?: string;
  hideMeta?: boolean;
}) {
  const shown = hideMeta ? lines.filter((l) => l.kind !== "meta") : lines;
  if (shown.length === 0) return <p className="px-3 py-2 text-xs text-muted-foreground">No textual changes.</p>;
  return (
    <div className={cn("overflow-auto font-mono text-xs leading-5", className)}>
      <div className="min-w-max">
        {shown.map((line, i) => (
          <div key={i} className={cn("flex px-3 whitespace-pre", styles[line.kind])}>
            <span className="w-4 shrink-0 select-none text-muted-foreground/70">{marks[line.kind]}</span>
            <span>{line.text || " "}</span>
          </div>
        ))}
      </div>
    </div>
  );
}

export function DiffStat({
  additions,
  deletions,
  className,
}: {
  additions: number;
  deletions: number;
  className?: string;
}) {
  return (
    <span className={cn("font-mono text-xs tabular-nums", className)}>
      <span className="text-emerald-600">+{additions}</span> <span className="text-red-600">−{deletions}</span>
    </span>
  );
}
