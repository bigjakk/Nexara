import { useEffect, useMemo } from "react";
import { Link } from "react-router-dom";
import { AlertTriangle, ShieldAlert, ShieldCheck, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import { cn } from "@/lib/utils";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import { useHealthDismissStore } from "@/stores/health-dismiss-store";
import {
  cephHealthLabel,
  cephSeverity,
  isProblem,
  severityDotClass,
  severityTextClass,
  worseSeverity,
  type CephSeverity,
} from "@/features/ceph/lib/ceph-health";
import type { ClusterResponse } from "@/types/api";

interface HealthIssue {
  key: string;
  clusterName: string;
  severity: CephSeverity;
  summary: string;
  details: string[];
  to: string;
}

/** Derives the connectivity + Ceph/storage issues for a single cluster. */
function clusterIssues(c: ClusterResponse): HealthIssue[] {
  const issues: HealthIssue[] = [];

  if (c.status === "offline") {
    issues.push({
      key: `${c.id}:conn`,
      clusterName: c.name,
      severity: "err",
      summary: "Cluster offline",
      details: ["No nodes reachable"],
      to: `/clusters/${c.id}`,
    });
  } else if (c.status === "degraded") {
    issues.push({
      key: `${c.id}:conn`,
      clusterName: c.name,
      severity: "warn",
      summary: "Cluster degraded",
      details: ["Some nodes offline"],
      to: `/clusters/${c.id}`,
    });
  }

  const sev = cephSeverity(c.ceph_health?.status);
  if (c.ceph_health && isProblem(sev)) {
    issues.push({
      key: `${c.id}:ceph`,
      clusterName: c.name,
      severity: sev,
      summary: `Ceph storage: ${cephHealthLabel(c.ceph_health.status)}`,
      details: c.ceph_health.checks.map((k) => k.message),
      to: `/clusters/${c.id}?tab=ceph`,
    });
  }

  return issues;
}

/**
 * A signature uniquely identifies an issue *and its state* so that dismissals
 * stick while the condition is unchanged but reappear if it escalates or its
 * reasons change.
 */
function issueSig(i: HealthIssue): string {
  // NB: joined with U+0001 (fields) / U+0002 (reasons) — control chars that
  // can't appear in ids or messages, so distinct issue states never collide.
  // They render invisibly in editors/diffs; the \u escapes below are real.
  return `${i.key}\u0001${i.severity}\u0001${i.details.join("\u0002")}`;
}

/**
 * GlobalHealthIndicator is the always-visible header pill that surfaces
 * infrastructure problems (cluster connectivity + Ceph/storage health) so users
 * don't have to dig into a submenu to discover them. Issues can be dismissed;
 * dismissals are remembered until the underlying condition resolves.
 */
export function GlobalHealthIndicator() {
  const { data: clusters } = useClusters();
  const dismissed = useHealthDismissStore((s) => s.dismissed);
  const dismiss = useHealthDismissStore((s) => s.dismiss);
  const restoreAll = useHealthDismissStore((s) => s.restoreAll);
  const syncActive = useHealthDismissStore((s) => s.syncActive);

  const allIssues = useMemo(
    () =>
      (clusters ?? [])
        .flatMap(clusterIssues)
        .map((i) => ({ ...i, sig: issueSig(i) })),
    [clusters],
  );

  // Forget dismissals whose issue has resolved (so recurrences re-show). Only
  // once clusters have actually loaded, to avoid wiping on the initial empty state.
  useEffect(() => {
    if (clusters === undefined) return;
    syncActive(allIssues.map((i) => i.sig));
  }, [clusters, allIssues, syncActive]);

  const dismissedSet = useMemo(() => new Set(dismissed), [dismissed]);
  const visible = allIssues.filter((i) => !dismissedSet.has(i.sig));
  const hiddenCount = allIssues.length - visible.length;
  const hasVisible = visible.length > 0;

  const overall = visible.reduce<CephSeverity>(
    (acc, i) => worseSeverity(acc, i.severity),
    "ok",
  );

  const Icon =
    overall === "err"
      ? ShieldAlert
      : overall === "warn"
        ? AlertTriangle
        : ShieldCheck;

  const subtitle = hasVisible
    ? `${String(visible.length)} issue${visible.length === 1 ? "" : "s"} ${visible.length === 1 ? "needs" : "need"} attention`
    : hiddenCount > 0
      ? "All clear (dismissed issues hidden)"
      : "All systems healthy";

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
              <li key={issue.key} className="group/issue relative">
                <Link
                  to={issue.to}
                  className="block py-2 pl-3 pr-9 transition-colors hover:bg-accent/50"
                >
                  <div className="flex items-center gap-2">
                    <span
                      className={cn(
                        "h-2 w-2 shrink-0 rounded-full",
                        severityDotClass[issue.severity],
                      )}
                    />
                    <span className="truncate text-sm font-medium">
                      {issue.clusterName}
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
                  {issue.details.length > 0 && (
                    <ul className="mt-1 space-y-0.5 pl-4">
                      {issue.details.map((d, i) => (
                        <li
                          key={`${issue.key}:${String(i)}`}
                          className="truncate text-xs text-muted-foreground"
                        >
                          {d}
                        </li>
                      ))}
                    </ul>
                  )}
                </Link>
                <button
                  type="button"
                  aria-label={`Dismiss ${issue.clusterName} ${issue.summary}`}
                  title="Dismiss"
                  onClick={(e) => {
                    e.preventDefault();
                    e.stopPropagation();
                    dismiss(issue.sig);
                  }}
                  className="absolute right-1.5 top-2 rounded p-1 text-muted-foreground opacity-0 transition-opacity hover:bg-accent hover:text-foreground focus-visible:opacity-100 group-hover/issue:opacity-100"
                >
                  <X className="h-3.5 w-3.5" />
                </button>
              </li>
            ))}
          </ul>
        ) : (
          <div className="flex items-center gap-2 px-3 py-6 text-sm text-muted-foreground">
            <ShieldCheck className="h-4 w-4 text-emerald-500" />
            {hiddenCount > 0
              ? "No active issues."
              : "Everything looks healthy."}
          </div>
        )}

        {hiddenCount > 0 && (
          <div className="flex items-center justify-between border-t px-3 py-2">
            <span className="text-xs text-muted-foreground">
              {hiddenCount} dismissed
            </span>
            <button
              type="button"
              aria-label={`Restore ${String(hiddenCount)} dismissed issue${hiddenCount === 1 ? "" : "s"}`}
              onClick={() => {
                restoreAll();
              }}
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
