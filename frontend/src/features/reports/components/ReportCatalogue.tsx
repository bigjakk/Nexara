import { Link } from "react-router-dom";
import { Button } from "@/components/ui/button";
import { Panel } from "@/components/page/Panel";
import { CalendarClock } from "lucide-react";
import { formatDateTime, formatExactDateTime } from "@/lib/format";
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
 * One entry per report type: what it answers, what is inside, and the most
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
    <Panel
      title="Report types"
      aside={`${String(REPORT_TYPES.length)} to choose from`}
    >
      <ul className="divide-y">
        {REPORT_TYPES.map((info) => (
          <CatalogueEntry
            key={info.value}
            info={info}
            latest={latestByType.get(info.value)}
            canGenerate={canGenerate}
            canManage={canManage}
            onGenerate={onGenerate}
            onSchedule={onSchedule}
          />
        ))}
      </ul>
    </Panel>
  );
}

function CatalogueEntry({
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
    <li className="py-3.5 first:pt-0 last:pb-0">
      <div className="flex items-start gap-2">
        <h3 className="min-w-0 flex-1 text-sm font-semibold leading-7">
          {info.label}
        </h3>
        {canManage && (
          <Button
            variant="ghost"
            size="icon"
            className="h-7 w-7 shrink-0 text-muted-foreground"
            onClick={() => {
              onSchedule(info.value);
            }}
            title="Schedule this report"
            aria-label={`Schedule ${info.label}`}
          >
            <CalendarClock className="h-3.5 w-3.5" />
          </Button>
        )}
        {canGenerate && (
          <Button
            variant="outline"
            size="sm"
            className="h-7 shrink-0 px-2.5"
            aria-label={`Generate ${info.label}`}
            onClick={() => {
              onGenerate(info.value);
            }}
          >
            Generate
          </Button>
        )}
      </div>
      <p className="mt-0.5 text-[13px] text-muted-foreground">{info.blurb}</p>
      <div className="mt-1.5 flex flex-wrap items-baseline gap-x-4 gap-y-1 text-[12.5px]">
        {latest ? (
          <Link
            to={`/reports/runs/${latest.id}`}
            className="text-primary hover:underline"
            title={formatExactDateTime(latest.created_at)}
          >
            Open the run from {formatDateTime(latest.created_at)}
          </Link>
        ) : (
          <span className="text-muted-foreground/75">Never run</span>
        )}
        <details className="min-w-0 basis-full text-muted-foreground">
          <summary className="w-fit cursor-pointer rounded-sm outline-hidden hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring">
            What is inside
          </summary>
          <ul className="mt-1 list-disc space-y-0.5 pl-4">
            {info.sections.map((s) => (
              <li key={s}>{s}</li>
            ))}
          </ul>
        </details>
      </div>
    </li>
  );
}
