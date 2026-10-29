"use client";

import { createContext, useContext } from "react";

/** The session's WorkDir, so tool cards can show paths relative to it. */
export const WorkDirContext = createContext("");

export function useRelativePath(): (path: string) => string {
  const workDir = useContext(WorkDirContext).replace(/\/+$/, "");
  return (path) => (workDir && path.startsWith(workDir + "/") ? path.slice(workDir.length + 1) : path);
}
