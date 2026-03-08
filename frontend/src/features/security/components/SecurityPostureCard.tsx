import { ShieldCheck, ShieldAlert, ShieldX, Shield } from "lucide-react";
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
