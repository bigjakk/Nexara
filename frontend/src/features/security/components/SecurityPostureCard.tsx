import { ShieldCheck, ShieldAlert, ShieldX, Shield, Flame } from "lucide-react";
import { cn } from "@/lib/utils";
import type { SecurityPosture } from "@/types/api";
import { SeverityBadge } from "./SeverityBadge";

interface SecurityPostureCardProps {
  posture: SecurityPosture;
}

export function SecurityPostureCard({ posture }: SecurityPostureCardProps) {
  const score = posture.posture_score;
  const hasScans = posture.status !== "no_scans";

  const getScoreColor = (s: number) => {
    if (s >= 90) return "text-green-500";
    if (s >= 70) return "text-yellow-500";
    if (s >= 40) return "text-orange-500";
    return "text-red-500";
  };

  const getScoreIcon = (s: number) => {
    if (s >= 90) return ShieldCheck;
    if (s >= 70) return Shield;
    if (s >= 40) return ShieldAlert;
    return ShieldX;
  };

  const ScoreIcon = getScoreIcon(score);

  return (
    <div className="rounded-lg border bg-card p-6">
      <div className="flex items-center justify-between">
        <div>
          <h3 className="text-sm font-medium text-muted-foreground">
            Security Posture
          </h3>
          <div className={cn("mt-1 flex items-center gap-2", getScoreColor(score))}>
            <ScoreIcon className="h-8 w-8" />
            <span className="text-3xl font-bold">{Math.round(score)}</span>
            <span className="text-sm text-muted-foreground">/100</span>
          </div>
        </div>

        {hasScans && (
          <div className="text-right text-sm text-muted-foreground">
            <p>{posture.scanned_nodes}/{posture.total_nodes} nodes scanned</p>
            <p>{posture.total_vulns} vulnerabilities found</p>
          </div>
        )}
      </div>

      {hasScans && posture.total_vulns > 0 && (
        <div className="mt-4 flex flex-wrap gap-2">
          {posture.critical_count > 0 && (
            <SeverityBadge severity="critical" count={posture.critical_count} />
          )}
          {posture.high_count > 0 && (
            <SeverityBadge severity="high" count={posture.high_count} />
          )}
          {posture.medium_count > 0 && (
            <SeverityBadge severity="medium" count={posture.medium_count} />
          )}
          {posture.low_count > 0 && (
            <SeverityBadge severity="low" count={posture.low_count} />
          )}
          {posture.unknown_count > 0 && (
            <SeverityBadge severity="unknown" count={posture.unknown_count} />
          )}
        </div>
      )}

      {hasScans && posture.kev_count > 0 && (
        <div className="mt-4 flex items-start gap-2 rounded-md border border-red-500/30 bg-red-500/10 p-3 text-sm">
          <Flame className="mt-0.5 h-4 w-4 shrink-0 text-red-500" />
          <div>
            <div className="font-semibold text-red-700 dark:text-red-400">
              {posture.kev_count} actively exploited{" "}
              {posture.kev_count === 1 ? "vulnerability" : "vulnerabilities"}
            </div>
            <div className="text-xs text-muted-foreground">
              Listed in CISA's Known Exploited Vulnerabilities catalog —
              patch immediately.
            </div>
          </div>
        </div>
      )}

      {!hasScans && (
        <p className="mt-2 text-sm text-muted-foreground">
          No scans have been run yet. Click &quot;Scan Now&quot; to check for vulnerabilities.
        </p>
      )}
    </div>
  );
}
