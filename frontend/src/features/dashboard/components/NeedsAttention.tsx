import { Link } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { AlertTriangle, BellOff, CircleX } from "lucide-react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { useHealthIssues } from "@/hooks/useHealthIssues";

/**
 * What needs the operator, first on the dashboard: the same open issues the
 * header pill counts, each with its detail and the way to its page. Renders
 * nothing while there is none, so a healthy estate opens on the fleet.
 */
export function NeedsAttention() {
  const { t } = useTranslation("dashboard");
  const { visible, dismiss, mute } = useHealthIssues();

  if (visible.length === 0) return null;

  return (
    <section aria-labelledby="needs-attention-heading">
      <div className="mb-2 flex items-baseline gap-2.5">
        <h2 id="needs-attention-heading" className="text-[15px] font-semibold">
          {t("needsAttention")}
        </h2>
        <span className="text-sm text-muted-foreground">
          {t("needsAttentionOpen", { count: visible.length })}
        </span>
      </div>
      <ul className="divide-y overflow-hidden rounded-lg border bg-card">
        {visible.map((issue) => {
          const failed = issue.severity === "err";
          const Icon = failed ? CircleX : AlertTriangle;
          return (
            <li
              key={issue.sig}
              className="flex flex-wrap items-center gap-x-3.5 gap-y-2 px-3.5 py-3"
            >
              <span
                aria-hidden="true"
                className={cn(
                  "flex h-8 w-8 shrink-0 items-center justify-center rounded-lg",
                  failed
                    ? "bg-red-500/15 text-red-700 dark:text-red-400"
                    : "bg-amber-500/15 text-amber-700 dark:text-amber-400",
                )}
              >
                <Icon className="h-4 w-4" />
              </span>
              <div className="min-w-0 flex-[1_1_18rem]">
                <p className="text-sm font-medium">
                  {issue.summary}
                  {issue.target !== "" && (
                    <span className="text-muted-foreground">
                      {" "}
                      {issue.target}
                    </span>
                  )}
                </p>
                <p className="text-xs text-muted-foreground">
                  <span className="font-medium">{issue.clusterName}</span>
                  {issue.detail !== "" && <> {issue.detail}</>}
                </p>
              </div>
              <Button asChild variant="outline" size="sm">
                <Link to={issue.to}>{t("issueOpen")}</Link>
              </Button>
              <Button
                variant="ghost"
                size="sm"
                aria-label={`Dismiss ${issue.summary} on ${issue.clusterName}`}
                onClick={() => {
                  dismiss(issue.sig);
                }}
              >
                {t("issueDismiss")}
              </Button>
              <Button
                variant="ghost"
                size="sm"
                aria-label={`Mute ${issue.summary} alerts`}
                title={t("issueMuteHint")}
                onClick={() => {
                  mute(issue.type);
                }}
              >
                <BellOff />
                {t("issueMute")}
              </Button>
            </li>
          );
        })}
      </ul>
    </section>
  );
}
