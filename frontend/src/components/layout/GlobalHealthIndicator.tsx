import { useEffect } from "react";
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
import { useHealthIssues } from "@/hooks/useHealthIssues";
import { useHealthDismissStore } from "@/stores/health-dismiss-store";
import {
  severityDotClass,
  severityTextClass,
} from "@/features/ceph/lib/ceph-health";

/**
 * GlobalHealthIndicator is the always-visible header pill that surfaces the
 * open infrastructure issues (see useHealthIssues). Each issue can be dismissed
 * (this occurrence, until it resolves) or its whole type muted (suppressed
 * everywhere until restored).
 */
export function GlobalHealthIndicator() {
  const {
    all,
    loaded,
    visible,
    hiddenCount,
    mutedTypes,
    overall,
    dismiss,
    mute,
    restoreAll,
  } = useHealthIssues();
  const syncActive = useHealthDismissStore((s) => s.syncActive);

  // Forget dismissals whose issue has resolved (so recurrences re-show). Only
  // once every source has loaded, to avoid wiping on the initial empty state.
  // Here and nowhere else: this pill is mounted on every page, so one owner is
  // enough, and the dashboard's list reads the same issues without syncing.
  useEffect(() => {
    if (!loaded) return;
    syncActive(all.map((i) => i.sig));
  }, [loaded, all, syncActive]);

  const hasVisible = visible.length > 0;
  const hasSuppressed = hiddenCount > 0 || mutedTypes.length > 0;

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

  return (
    <Popover>
      <PopoverTrigger asChild>
        {/* Unlit while healthy; with something open it is the one coloured
            thing in the bar and says how many. */}
        <Button
          variant="ghost"
          size={hasVisible ? "sm" : "icon"}
          className={cn(
            "shrink-0",
            hasVisible &&
              (overall === "err"
                ? "gap-1.5 bg-red-500/15 px-2.5 font-semibold text-red-700 hover:bg-red-500/25 hover:text-red-700 dark:text-red-400 dark:hover:text-red-400"
                : "gap-1.5 bg-amber-500/15 px-2.5 font-semibold text-amber-700 hover:bg-amber-500/25 hover:text-amber-700 dark:text-amber-400 dark:hover:text-amber-400"),
          )}
          aria-label={
            hasVisible
              ? `Infrastructure health: ${String(visible.length)} issue(s)`
              : "Infrastructure health: all healthy"
          }
        >
          <Icon
            className={cn(
              hasVisible ? "h-4 w-4" : "h-5 w-5 text-muted-foreground",
              overall === "err" && "animate-pulse",
            )}
          />
          {hasVisible && (
            <>
              {visible.length}
              <span className="hidden lg:inline">
                {visible.length === 1 ? "needs" : "need"} attention
              </span>
            </>
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
                <div className="absolute right-1.5 top-1.5 flex items-center gap-0.5 opacity-0 transition-opacity focus-within:opacity-100 group-hover/issue:opacity-100 pointer-coarse:opacity-100">
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
