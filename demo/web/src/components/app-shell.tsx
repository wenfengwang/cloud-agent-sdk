"use client";

import { Toaster } from "@/components/ui/sonner";
import { TooltipProvider } from "@/components/ui/tooltip";
import { AppProvider } from "@/lib/state/app";
import { ConnectionBanner } from "./connection-banner";
import { DeleteSessionProvider } from "./delete-session";
import { Sidebar } from "./sidebar";

export function AppShell({ children }: { children: React.ReactNode }) {
  return (
    <AppProvider>
      <TooltipProvider delayDuration={300}>
        <DeleteSessionProvider>
          <div className="flex h-screen overflow-hidden">
            <Sidebar />
            <main className="flex min-w-0 flex-1 flex-col">
              <ConnectionBanner />
              <div className="min-h-0 flex-1">{children}</div>
            </main>
          </div>
          <Toaster position="bottom-right" />
        </DeleteSessionProvider>
      </TooltipProvider>
    </AppProvider>
  );
}
