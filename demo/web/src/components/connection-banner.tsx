"use client";

import { WifiOffIcon } from "lucide-react";
import { Button } from "@/components/ui/button";
import { useApp } from "@/lib/state/app";

export function ConnectionBanner() {
  const { connection, events, configError } = useApp();
  if (connection !== "lost" && !configError) return null;
  return (
    <div className="flex items-center justify-center gap-3 border-b border-amber-200 bg-amber-50 px-4 py-2 text-sm text-amber-950">
      <WifiOffIcon className="size-4 text-amber-600" />
      <span>Connection to the backend lost. Reconnecting…</span>
      <Button size="xs" variant="outline" onClick={() => events.reconnect()}>
        Reconnect
      </Button>
    </div>
  );
}
