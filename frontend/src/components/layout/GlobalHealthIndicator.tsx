import { useEffect, useMemo } from "react";
import { Link } from "react-router-dom";
import {
  AlertTriangle,
  BellOff,
  ShieldAlert,
  ShieldCheck,
  X,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import { cn } from "@/lib/utils";
import { issueSig } from "@/lib/health-issues";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import { useHealthDismissStore } from "@/stores/health-dismiss-store";
import { useHealthMuteStore } from "@/stores/health-mute-store";
import {
  severityDotClass,
  severityTextClass,
  worstIssueSeverity,
} from "@/features/ceph/lib/ceph-health";
import type { HealthIssue } from "@/types/api";

interface FlatIssue extends HealthIssue {
  clusterId: string;
  clusterName: string;
  sig: string;
  to: string;
}

function issueLink(clusterId: string, i: HealthIssue): string {
  if (i.type === "ceph") return `/clusters/${clusterId}?tab=ceph`;
  return `/clusters/${clusterId}`;
}

/**
 * GlobalHealthIndicator is the always-visible header pill that surfaces
 * infrastructure problems (Ceph, HA, disks, storage, failed tasks, …) computed
 * server-side and attached to each cluster as issues[]. Each issue can be
 * dismissed (this occurrence, until it resolves) or its whole type muted
 * (suppressed everywhere until restored).
 */
export function GlobalHealthIndicator() {
  const { data: clusters } = useClusters();
  const dismissed = useHealthDismissStore((s) => s.dismissed);
  const dismiss = useHealthDismissStore((s) => s.dismiss);
  const restoreDismissed = useHealthDismissStore((s) => s.restoreAll);
  const syncActive = useHealthDismissStore((s) => s.syncActive);
  const mutedTypes = useHealthMuteStore((s) => s.mutedTypes);
  const mute = useHealthMuteStore((s) => s.mute);
  const restoreMuted = useHealthMuteStore((s) => s.restoreAll);

  const allIssues = useMemo<FlatIssue[]>(
    () =>
      (clusters ?? []).flatMap((c) =>
        (c.issues ?? []).map((iss) => ({
          ...iss,
          clusterId: c.id,
          clusterName: c.name,
          sig: issueSig(c.id, iss),
          to: issueLink(c.id, iss),
        })),
      ),
    [clusters],
  );

  // Forget dismissals whose issue has resolved (so recurrences re-show). Only
  // once clusters have loaded, to avoid wiping on the initial empty state.
  useEffect(() => {
    if (clusters === undefined) return;
    syncActive(allIssues.map((i) => i.sig));
  }, [clusters, allIssues, syncActive]);

  const dismissedSet = useMemo(() => new Set(dismissed), [dismissed]);
  const mutedSet = useMemo(() => new Set(mutedTypes), [mutedTypes]);
  const visible = allIssues.filter(
    (i) => !mutedSet.has(i.type) && !dismissedSet.has(i.sig),
  );
  const hiddenCount = allIssues.length - visible.length;
  const hasVisible = visible.length > 0;
  const hasSuppressed = hiddenCount > 0 || mutedTypes.length > 0;
  const overall = worstIssueSeverity(visible);

  const Icon =
    overall === "err"
      ? ShieldAlert
      : overall === "warn"
        ? AlertTriangle
        : ShieldCheck;

  const subtitle = hasVisible
    ? `${String(visible.length)} issue${visible.length === 1 ? "" : "s"} ${visible.length === 1 ? "needs" : "need"} attention`
    : hasSuppressed
      ? "All clear (some alerts suppressed)"
      : "All systems healthy";

  const restoreAll = () => {
    restoreDismissed();
    restoreMuted();
  };

  return (
    <Popover>
      <PopoverTrigger asChild>
        <Button
          variant="ghost"
          size="icon"
          className="relative shrink-0"
          aria-label={
            hasVisible
              ? `Infrastructure health: ${String(visible.length)} issue(s)`
              : "Infrastructure health: all healthy"
          }
        >
          <Icon
            className={cn(
              "h-5 w-5",
              hasVisible ? severityTextClass[overall] : "text-muted-foreground",
              overall === "err" && "animate-pulse",
            )}
          />
          {hasVisible && (
            <span
              className={cn(
                "absolute right-1 top-1 h-2 w-2 rounded-full ring-2 ring-card",
                severityDotClass[overall],
              )}
            />
          )}
        </Button>
      </PopoverTrigger>
      <PopoverContent align="end" className="w-80 p-0">
        <div className="border-b px-3 py-2">
          <p className="text-sm font-semibold">Infrastructure health</p>
          <p className="text-xs text-muted-foreground">{subtitle}</p>
        </div>

        {hasVisible ? (
          <ul className="max-h-96 overflow-auto py-1">
            {visible.map((issue) => (
              <li key={issue.sig} className="group/issue relative">
                <Link
                  to={issue.to}
                  className="block py-2 pl-3 pr-14 transition-colors hover:bg-accent/50"
                >
                  <div className="flex items-center gap-2">
                    <span
                      className={cn(
                        "h-2 w-2 shrink-0 rounded-full",
                        severityDotClass[issue.severity],
                      )}
                    />
                    <span className="truncate text-sm">
                      <span className="font-medium">{issue.clusterName}</span>
                      {issue.target !== "" && (
                        <span className="text-muted-foreground">
                          {" "}
                          · {issue.target}
                        </span>
                      )}
                    </span>
                    <span
                      className={cn(
                        "ml-auto shrink-0 text-xs",
                        severityTextClass[issue.severity],
                      )}
                    >
                      {issue.summary}
                    </span>
                  </div>
                  <p className="mt-1 truncate pl-4 text-xs text-muted-foreground">
                    {issue.detail}
                  </p>
                </Link>
                <div className="absolute right-1.5 top-1.5 flex items-center gap-0.5 opacity-0 transition-opacity focus-within:opacity-100 group-hover/issue:opacity-100">
                  <button
                    type="button"
                    aria-label={`Mute ${issue.summary} alerts`}
                    title="Mute this alert type"
                    onClick={(e) => {
                      e.preventDefault();
                      e.stopPropagation();
                      mute(issue.type);
                    }}
                    className="rounded p-1 text-muted-foreground hover:bg-accent hover:text-foreground"
                  >
                    <BellOff className="h-3.5 w-3.5" />
                  </button>
                  <button
                    type="button"
                    aria-label={`Dismiss ${issue.summary} on ${issue.clusterName}`}
                    title="Dismiss"
                    onClick={(e) => {
                      e.preventDefault();
                      e.stopPropagation();
                      dismiss(issue.sig);
                    }}
                    className="rounded p-1 text-muted-foreground hover:bg-accent hover:text-foreground"
                  >
                    <X className="h-3.5 w-3.5" />
                  </button>
                </div>
              </li>
            ))}
          </ul>
        ) : (
          <div className="flex items-center gap-2 px-3 py-6 text-sm text-muted-foreground">
            <ShieldCheck className="h-4 w-4 text-emerald-500" />
            {hasSuppressed ? "No active issues." : "Everything looks healthy."}
          </div>
        )}

        {hasSuppressed && (
          <div className="flex items-center justify-between border-t px-3 py-2">
            <span className="text-xs text-muted-foreground">
              {hiddenCount > 0
                ? `${String(hiddenCount)} hidden`
                : `${String(mutedTypes.length)} alert type${mutedTypes.length === 1 ? "" : "s"} muted`}
            </span>
            <button
              type="button"
              aria-label="Restore dismissed and muted alerts"
              onClick={restoreAll}
              className="text-xs font-medium text-primary hover:underline"
            >
              Restore
            </button>
          </div>
        )}
      </PopoverContent>
    </Popover>
  );
}
