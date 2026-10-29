"use client";

import { useRouter, useSearchParams } from "next/navigation";
import { useEffect, useState } from "react";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { ApiError, api } from "@/lib/api/client";
import type { HarnessId, Project } from "@/lib/api/types";
import { readPreference, useApp, writePreference } from "@/lib/state/app";
import { Composer, LocalLabel, ModelPicker, ProjectPicker, RunModeControls, WorktreeControl } from "./composer";
import { HarnessDot, harnessLabel } from "./harness-badge";
import { HarnessNotice, LoginHint } from "./harness-notice";

export function Home() {
  const app = useApp();
  const router = useRouter();
  const params = useSearchParams();
  const wantedProject = params.get("project");

  const [harness, setHarness] = useState<HarnessId>(() =>
    readPreference("harness") === "codex" ? "codex" : "claudecode",
  );
  const [projects, setProjects] = useState<Project[]>([]);
  const [project, setProject] = useState<Project | null>(null);
  const [branchInfo, setBranchInfo] = useState<{ path: string; current: string; branches: string[] } | null>(null);
  const [worktree, setWorktree] = useState(false);
  const [chosenBase, setChosenBase] = useState<string | null>(null);
  const [model, setModel] = useState("");
  const [prompt, setPrompt] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);

  useEffect(() => {
    api
      .projects()
      .then((list) => {
        setProjects(list);
        const preferred = wantedProject ?? readPreference("project");
        const found = list.find((p) => p.path === preferred);
        if (found) setProject(found);
        else if (preferred && wantedProject) {
          setProject({
            path: preferred,
            name: preferred.split("/").pop() || preferred,
            isGitRepository: false,
            currentBranch: "",
          });
        } else setProject(list[0] ?? null);
      })
      .catch((err) => setError(String(err.message ?? err)));
  }, [wantedProject]);

  // Branches tell whether the project is a git repository.
  useEffect(() => {
    if (!project) return;
    let live = true;
    api
      .branches(project.path)
      .then((b) => live && setBranchInfo({ path: project.path, ...b }))
      .catch(() => {});
    return () => {
      live = false;
    };
  }, [project]);
  const branches = branchInfo && branchInfo.path === project?.path ? branchInfo.branches : [];
  const baseBranch = chosenBase ?? (branchInfo && branchInfo.path === project?.path ? branchInfo.current : "");

  const chooseProject = (p: Project) => {
    setProject(p);
    setWorktree(false);
    setChosenBase(null);
  };

  const harnessInfo = app.harness(harness);
  const canSend = !!project && !creating && harnessInfo?.available !== false;

  const submit = async () => {
    if (!project || !prompt.trim()) return;
    setCreating(true);
    setError(null);
    try {
      const session = await api.createSession({
        harness,
        workDir: project.path,
        model,
        ...(worktree && branches.length > 0 ? { worktree: { enabled: true, baseBranch } } : {}),
      });
      writePreference("project", project.path);
      app.stashPrompt(session.id, prompt);
      router.push(`/?session=${encodeURIComponent(session.id)}`);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : String(err));
      setCreating(false);
    }
  };

  return (
    <div className="flex h-full flex-col items-center justify-center overflow-y-auto px-6 pb-[12vh]">
      <div className="w-full max-w-2xl space-y-8">
        <div className="space-y-2 text-center">
          <h1 className="text-3xl font-semibold tracking-tight">
            What should we build
            {project ? (
              <>
                {" "}
                in <span className="text-muted-foreground">{project.name}</span>
              </>
            ) : null}
            ?
          </h1>
          <p className="text-sm text-muted-foreground">
            Claude Code and Codex, driven through one SDK on this machine.
          </p>
        </div>

        <div className="space-y-3">
          <div className="flex justify-center">
            <ToggleGroup
              type="single"
              variant="outline"
              size="sm"
              value={harness}
              onValueChange={(v) => {
                if (v !== "claudecode" && v !== "codex") return;
                setHarness(v);
                setModel("");
                writePreference("harness", v);
              }}
            >
              {(["claudecode", "codex"] as const).map((h) => (
                <ToggleGroupItem key={h} value={h} className="gap-2 px-3">
                  <HarnessDot harness={h} />
                  {harnessLabel[h]}
                </ToggleGroupItem>
              ))}
            </ToggleGroup>
          </div>

          <Composer
            value={prompt}
            onChange={setPrompt}
            onSubmit={submit}
            running={false}
            locked={creating}
            canSend={canSend}
            placeholder={
              project ? `Ask ${harnessLabel[harness]} to do something in ${project.name}` : "Choose a project first"
            }
            error={error}
            notice={<HarnessNotice harness={harness} />}
            autoFocus
            toolbar={
              <>
                <ProjectPicker projects={projects} value={project} onChange={chooseProject} />
                {branches.length > 0 && (
                  <WorktreeControl
                    branches={branches}
                    enabled={worktree}
                    onEnabledChange={setWorktree}
                    baseBranch={baseBranch}
                    onBaseBranchChange={setChosenBase}
                  />
                )}
                <ModelPicker harness={harness} value={model} onChange={setModel} />
              </>
            }
          />
          <div className="flex items-center justify-center gap-0.5">
            <LocalLabel />
            <RunModeControls harness={harness} />
          </div>
          <LoginHint harness={harness} />
        </div>
      </div>
    </div>
  );
}
