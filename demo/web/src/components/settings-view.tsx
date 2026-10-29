"use client";

import { CheckCircle2Icon, TriangleAlertIcon, XCircleIcon } from "lucide-react";
import { useApp } from "@/lib/state/app";
import { CopyButton } from "./copy-button";
import { HarnessDot } from "./harness-badge";

function Card({ title, description, children }: { title: string; description?: string; children: React.ReactNode }) {
  return (
    <section className="rounded-xl border bg-background">
      <div className="border-b px-5 py-4">
        <h2 className="text-sm font-semibold">{title}</h2>
        {description && <p className="mt-0.5 text-sm text-muted-foreground">{description}</p>}
      </div>
      <div className="px-5 py-4">{children}</div>
    </section>
  );
}

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="grid grid-cols-[10rem_1fr] items-start gap-4 py-1.5 text-sm">
      <span className="text-muted-foreground">{label}</span>
      <div className="min-w-0 break-words">{children}</div>
    </div>
  );
}

export function SettingsView() {
  const app = useApp();
  const config = app.config;
  if (!config) return <div className="p-10 text-sm text-muted-foreground">Loading settings…</div>;
  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto max-w-3xl space-y-6 px-6 py-10">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">Settings</h1>
          <p className="mt-1 text-sm text-muted-foreground">
            Read-only. Configure the backend with its command-line flags.
          </p>
        </div>

        <Card title="Harnesses" description={`Home mode: ${config.homeMode}`}>
          <div className="divide-y">
            {config.harnesses.map((h) => {
              const mismatch = h.pinnedVersion && h.version !== h.pinnedVersion;
              return (
                <div key={h.harness} className="py-3 first:pt-0 last:pb-0">
                  <div className="mb-1 flex items-center gap-2 font-medium">
                    <HarnessDot harness={h.harness} className="size-2" />
                    {h.label}
                    {h.available ? (
                      <span className="flex items-center gap-1 text-xs font-normal text-emerald-700">
                        <CheckCircle2Icon className="size-3.5" /> Available
                      </span>
                    ) : (
                      <span className="flex items-center gap-1 text-xs font-normal text-red-700">
                        <XCircleIcon className="size-3.5" /> Not found
                      </span>
                    )}
                  </div>
                  <Row label="Version">
                    {h.version || "—"}
                    {mismatch && (
                      <span className="ml-2 inline-flex items-center gap-1 text-xs text-amber-700">
                        <TriangleAlertIcon className="size-3.5" /> pinned {h.pinnedVersion}
                      </span>
                    )}
                  </Row>
                  <Row label="Pinned version">
                    {h.pinnedVersion || (
                      <span className="text-muted-foreground">Not configured · {app.gapReason("pinned-versions")}</span>
                    )}
                  </Row>
                  <Row label="Home">
                    <code className="font-mono text-xs">{h.home}</code>
                  </Row>
                  <Row label="Log in">
                    <span className="inline-flex max-w-full items-center gap-1">
                      <code className="truncate rounded bg-muted px-1.5 py-0.5 font-mono text-xs">{h.loginHint}</code>
                      <CopyButton text={h.loginHint} />
                    </span>
                  </Row>
                  <Row label="Model suggestions">
                    {h.modelSuggestions.length ? (
                      h.modelSuggestions.join(", ")
                    ) : (
                      <span className="text-muted-foreground">None configured</span>
                    )}
                  </Row>
                </div>
              );
            })}
          </div>
        </Card>

        <Card title="Projects">
          <Row label="Project roots">
            {config.projectRoots.length ? (
              config.projectRoots.map((r) => (
                <code key={r} className="block font-mono text-xs">
                  {r}
                </code>
              ))
            ) : (
              <span className="text-muted-foreground">None; pass --project-root</span>
            )}
          </Row>
          <Row label="Worktree root">
            <code className="font-mono text-xs">{config.worktreeRoot}</code>
          </Row>
        </Card>

        <Card title="Storage">
          <p className="text-sm text-muted-foreground">
            Sessions live in the backend&apos;s memory only. {app.gapReason("index-location")}
          </p>
        </Card>

        <Card
          title="SDK capabilities"
          description="Features a desktop coding app would have that the SDK does not support yet. They appear as disabled controls."
        >
          <div className="divide-y text-sm">
            {config.gaps.map((g) => (
              <div key={g.id} className="grid grid-cols-[10rem_1fr] gap-4 py-2">
                <code className="font-mono text-xs text-muted-foreground">{g.id}</code>
                <span>{g.reason}</span>
              </div>
            ))}
          </div>
        </Card>
      </div>
    </div>
  );
}
