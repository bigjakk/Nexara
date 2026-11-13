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
      <PopoverContent
        align="end"
        className="w-[22rem] max-w-[calc(100vw-1rem)] p-0"
      >
        <div className="px-[18px] pb-3 pt-3.5">
          <p className="text-[15px] font-semibold">Infrastructure health</p>
          <p className="mt-0.5 text-[12.5px] text-muted-foreground">
            {subtitle}
          </p>
        </div>

        {hasVisible ? (
          <ul className="max-h-96 divide-y overflow-auto border-t">
            {visible.map((issue) => (
              <li key={issue.sig} className="group/issue relative">
                <Link
                  to={issue.to}
                  className="block py-2.5 pl-[18px] pr-16 outline-hidden transition-colors hover:bg-foreground/[0.04] focus-visible:bg-foreground/[0.06]"
                >
                  <div className="flex items-baseline gap-2">
                    <span
                      className={cn(
                        "h-2 w-2 shrink-0 self-center rounded-full",
                        severityDotClass[issue.severity],
                      )}
                    />
                    <span className="min-w-0 truncate text-sm">
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
                        "ml-auto shrink-0 text-[12.5px] font-medium",
                        severityTextClass[issue.severity],
                      )}
                    >
                      {issue.summary}
                    </span>
                  </div>
                  <p className="mt-0.5 truncate pl-4 text-[12.5px] text-muted-foreground">
                    {issue.detail}
                  </p>
                </Link>
                <div className="absolute right-2 top-2 flex items-center gap-0.5 rounded-md bg-popover opacity-0 transition-opacity focus-within:opacity-100 group-hover/issue:opacity-100 pointer-coarse:opacity-100">
                  <button
                    type="button"
                    aria-label={`Mute ${issue.summary} alerts`}
                    title="Mute this alert type"
                    onClick={(e) => {
                      e.preventDefault();
                      e.stopPropagation();
                      mute(issue.type);
                    }}
                    className="grid h-6 w-6 place-items-center rounded-md text-muted-foreground outline-hidden hover:bg-foreground/[0.08] hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring"
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
                    className="grid h-6 w-6 place-items-center rounded-md text-muted-foreground outline-hidden hover:bg-foreground/[0.08] hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring"
                  >
                    <X className="h-3.5 w-3.5" />
                  </button>
                </div>
              </li>
            ))}
          </ul>
        ) : (
          // Healthy is quiet: a dot and a plain sentence.
          <div className="flex items-center gap-2 border-t px-[18px] py-4 text-sm text-muted-foreground">
            <span
              aria-hidden="true"
              className="h-2 w-2 shrink-0 rounded-full bg-emerald-500"
            />
            {hasSuppressed ? "No active issues." : "Everything looks healthy."}
          </div>
        )}

        {hasSuppressed && (
          <div className="flex items-center justify-between border-t px-[18px] py-2.5">
            <span className="text-[12.5px] text-muted-foreground">
              {hiddenCount > 0
                ? `${String(hiddenCount)} hidden`
                : `${String(mutedTypes.length)} alert type${mutedTypes.length === 1 ? "" : "s"} muted`}
            </span>
            <button
              type="button"
              aria-label="Restore dismissed and muted alerts"
              onClick={restoreAll}
              className="rounded-sm text-[12.5px] font-medium text-primary outline-hidden hover:underline focus-visible:ring-2 focus-visible:ring-ring"
            >
              Restore
            </button>
          </div>
        )}
      </PopoverContent>
    </Popover>
  );
}
