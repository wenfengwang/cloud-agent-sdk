"use client";

import { useRouter, useSearchParams } from "next/navigation";
import { createContext, useContext, useState } from "react";
import { toast } from "sonner";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { api } from "@/lib/api/client";
import type { SessionSummary } from "@/lib/api/types";
import { useApp } from "@/lib/state/app";

const DeleteContext = createContext<(s: SessionSummary) => void>(() => {});

export function useDeleteSession() {
  return useContext(DeleteContext);
}

/** Confirms and deletes a session, reporting a kept worktree. */
export function DeleteSessionProvider({ children }: { children: React.ReactNode }) {
  const app = useApp();
  const router = useRouter();
  const params = useSearchParams();
  const [target, setTarget] = useState<SessionSummary | null>(null);

  const confirm = async (s: SessionSummary) => {
    if (params.get("session") === s.id) router.push("/");
    try {
      const res = await api.deleteSession(s.id);
      app.removeSession(s.id);
      if (res.worktree && !res.worktree.removed) {
        toast(`Worktree kept: ${res.worktree.reason ?? "it has local changes"}`, { description: res.worktree.path });
      }
    } catch (err) {
      toast.error("Delete failed", { description: String((err as Error).message ?? err) });
      app.refreshSessions().catch(() => {});
    }
  };

  return (
    <DeleteContext.Provider value={setTarget}>
      {children}
      <AlertDialog open={!!target} onOpenChange={(open) => !open && setTarget(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete “{target?.title || "New session"}”?</AlertDialogTitle>
            <AlertDialogDescription>
              The agent is stopped and the session is removed from this backend.
              {target?.worktree
                ? " Its worktree is removed only if it has no uncommitted changes; the branch is kept."
                : ""}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              onClick={() => {
                if (target) confirm(target);
                setTarget(null);
              }}
            >
              Delete
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </DeleteContext.Provider>
  );
}
