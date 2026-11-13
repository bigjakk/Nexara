import { cn } from "@/lib/utils";
import { cephSeverity, severityDotClass } from "../lib/ceph-health";
import type { CephHealthCheckItem } from "../types/ceph";

interface CephHealthChecksProps {
  checks: CephHealthCheckItem[];
  className?: string;
}

/** Renders the list of Ceph health checks (the reasons behind a warning/error). */
export function CephHealthChecks({ checks, className }: CephHealthChecksProps) {
  if (checks.length === 0) return null;

  return (
    <ul className={cn("space-y-2.5", className)}>
      {checks.map((c) => {
        const sev = cephSeverity(c.severity);
        return (
          <li key={c.type} className="text-sm">
            <div className="flex items-start gap-2.5">
              <span
                className={cn(
                  "mt-[7px] h-1.5 w-1.5 shrink-0 rounded-full",
                  severityDotClass[sev],
                )}
              />
              <span className="min-w-0 [overflow-wrap:anywhere]">
                <span className="font-medium">{c.message}</span>
                <span className="ml-2 font-mono text-[12px] text-muted-foreground">
                  {c.type}
                </span>
              </span>
            </div>
            {c.detail.length > 0 && (
              <ul className="ml-4 mt-1 space-y-0.5 text-[12.5px] leading-relaxed text-muted-foreground">
                {c.detail.map((d, i) => (
                  <li key={`${c.type}:${String(i)}`}>{d}</li>
                ))}
              </ul>
            )}
          </li>
        );
      })}
    </ul>
  );
}
