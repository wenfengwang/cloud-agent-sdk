"use client";

import { InfoIcon, TriangleAlertIcon } from "lucide-react";
import type { HarnessId } from "@/lib/api/types";
import { useApp } from "@/lib/state/app";
import { CopyButton } from "./copy-button";
import { harnessLabel } from "./harness-badge";

/** Shown above the composer when the harness CLI is missing or not the pinned version. */
export function HarnessNotice({ harness }: { harness: HarnessId }) {
  const info = useApp().harness(harness);
  if (!info) return null;
  if (!info.available) {
    return (
      <div className="flex items-start gap-2 rounded-lg border border-amber-200 bg-amber-50/70 px-3 py-2 text-sm text-amber-950">
        <TriangleAlertIcon className="mt-0.5 size-4 shrink-0 text-amber-600" />
        <p>
          The {harnessLabel[harness]} CLI was not found on the backend&apos;s PATH. Install it, or start the backend
          with <code className="font-mono text-xs">--harness-bin-dir</code>.
        </p>
      </div>
    );
  }
  if (info.pinnedVersion && info.version !== info.pinnedVersion) {
    return (
      <div className="flex items-start gap-2 rounded-lg border border-amber-200 bg-amber-50/70 px-3 py-2 text-sm text-amber-950">
        <TriangleAlertIcon className="mt-0.5 size-4 shrink-0 text-amber-600" />
        <p>
          {harnessLabel[harness]} {info.version} is installed, but the SDK is pinned to {info.pinnedVersion}.
        </p>
      </div>
    );
  }
  return null;
}

/** How to log the harness in, for the home it runs in. */
export function LoginHint({ harness }: { harness: HarnessId }) {
  const app = useApp();
  const info = app.harness(harness);
  if (!info?.available || !info.loginHint) return null;
  const isolated = app.config?.homeMode === "isolated";
  return (
    <div className="flex items-center justify-center gap-1.5 text-xs whitespace-nowrap text-muted-foreground">
      <InfoIcon className="size-3.5 shrink-0" />
      <span>{isolated ? "Isolated home · first run? Log in once:" : "Uses your own home · log in with:"}</span>
      <code className="max-w-[28rem] truncate rounded bg-muted px-1.5 py-0.5 font-mono text-[11px]">
        {info.loginHint}
      </code>
      <CopyButton text={info.loginHint} label="Copy login command" />
    </div>
  );
}
