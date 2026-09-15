import { Link } from "react-router-dom";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { CalendarClock, Play } from "lucide-react";
import { formatRelativeTime } from "@/lib/format";
import type { ReportRun } from "@/types/api";
import { REPORT_TYPES, type ReportTypeInfo } from "../report-types";

interface ReportCatalogueProps {
  runs: ReportRun[] | undefined;
  canGenerate: boolean;
  canManage: boolean;
  onGenerate: (type: string) => void;
  onSchedule: (type: string) => void;
}

/**
 * One card per report type: what it answers, what is inside, and the most
 * recent finished run so a reader can open it without regenerating.
 */
export function ReportCatalogue({
  runs,
  canGenerate,
  canManage,
  onGenerate,
  onSchedule,
}: ReportCatalogueProps) {
  const latestByType = new Map<string, ReportRun>();
  for (const run of runs ?? []) {
    if (run.status !== "completed") continue;
    const current = latestByType.get(run.report_type);
    if (!current || run.created_at > current.created_at) {
      latestByType.set(run.report_type, run);
    }
  }

  return (
    <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
      {REPORT_TYPES.map((info) => (
        <CatalogueCard
          key={info.value}
          info={info}
          latest={latestByType.get(info.value)}
          canGenerate={canGenerate}
          canManage={canManage}
          onGenerate={onGenerate}
          onSchedule={onSchedule}
        />
      ))}
    </div>
  );
}

function CatalogueCard({
  info,
  latest,
  canGenerate,
  canManage,
  onGenerate,
  onSchedule,
}: {
  info: ReportTypeInfo;
  latest: ReportRun | undefined;
  canGenerate: boolean;
  canManage: boolean;
  onGenerate: (type: string) => void;
  onSchedule: (type: string) => void;
}) {
  return (
    <Card className="flex flex-col">
      <CardContent className="flex flex-1 flex-col gap-2 p-4">
        <div>
          <h3 className="text-sm font-semibold leading-tight">{info.label}</h3>
          <p className="mt-1 text-xs text-muted-foreground">{info.blurb}</p>
        </div>
        <ul className="mt-1 list-disc space-y-0.5 pl-4 text-xs text-muted-foreground">
          {info.sections.map((s) => (
            <li key={s}>{s}</li>
          ))}
        </ul>
        <div className="mt-auto flex flex-wrap items-center justify-between gap-2 pt-2 text-xs">
          {latest ? (
            <Link
              to={`/reports/runs/${latest.id}`}
              className="text-primary hover:underline"
              title={new Date(latest.created_at).toLocaleString()}
            >
              Last run {formatRelativeTime(latest.created_at)} ›
            </Link>
          ) : (
            <span className="text-muted-foreground">Never run</span>
          )}
          <div className="flex gap-1">
            {canManage && (
              <Button
                variant="ghost"
                size="sm"
                className="h-7 px-2"
                onClick={() => {
                  onSchedule(info.value);
                }}
                title="Schedule this report"
              >
                <CalendarClock className="h-3.5 w-3.5" />
              </Button>
            )}
            {canGenerate && (
              <Button
                variant="outline"
                size="sm"
                className="h-7 px-2"
                onClick={() => {
                  onGenerate(info.value);
                }}
              >
                <Play className="mr-1 h-3.5 w-3.5" />
                Generate
              </Button>
            )}
          </div>
        </div>
      </CardContent>
    </Card>
  );
}
