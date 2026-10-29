import type { Usage } from "@/lib/api/types";

/** Formats milliseconds as "4s", "1m 3s", or "1h 2m". */
export function formatDuration(ms: number): string {
  const s = Math.max(0, Math.round(ms / 1000));
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ${s % 60}s`;
  return `${Math.floor(m / 60)}h ${m % 60}m`;
}

export function between(start: string, end: string | null, now = Date.now()): number {
  return (end ? Date.parse(end) : now) - Date.parse(start);
}

/** Formats a count as "412", "1.8k", or "2.1M". */
export function compact(n: number): string {
  if (n < 1000) return String(n);
  if (n < 1_000_000) return `${(n / 1000).toFixed(n < 10_000 ? 1 : 0).replace(/\.0$/, "")}k`;
  return `${(n / 1_000_000).toFixed(1).replace(/\.0$/, "")}M`;
}

/** The non-zero usage counts, such as "1.8k in · 412 out · 20k cache read". */
export function formatUsage(u: Usage | null): string {
  if (!u) return "";
  const parts: string[] = [];
  if (u.inputTokens) parts.push(`${compact(u.inputTokens)} in`);
  if (u.outputTokens) parts.push(`${compact(u.outputTokens)} out`);
  if (u.cacheReadTokens) parts.push(`${compact(u.cacheReadTokens)} cache read`);
  if (u.cacheWriteTokens) parts.push(`${compact(u.cacheWriteTokens)} cache write`);
  return parts.join(" · ");
}

export function initials(name: string): string {
  const parts = name
    .trim()
    .split(/[\s._-]+/)
    .filter(Boolean);
  return ((parts[0]?.[0] ?? "?") + (parts[1]?.[0] ?? "")).toUpperCase();
}

export function basename(path: string): string {
  const parts = path.replace(/\/+$/, "").split("/");
  return parts[parts.length - 1] || path;
}
