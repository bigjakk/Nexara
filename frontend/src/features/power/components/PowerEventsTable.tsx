import { Fragment, useMemo } from "react";
import { Link } from "react-router-dom";
import { TableBody, TableHeader, TableRow } from "@/components/ui/table";
import { formatDateTime } from "@/lib/format";
import { byId } from "@/hooks/useTableSort";
import type { ColumnDef } from "@/hooks/useColumnLayout";
import { useDataTable } from "@/hooks/useDataTable";
import { DataTableFrame } from "@/components/DataTableFrame";
import { DataTableHeadRow } from "@/components/DataTableHeadCells";
import { DataTableCells } from "@/components/DataTableCells";
import { DetailField } from "@/components/DetailField";
import { ExpandedDetailRow } from "@/components/ExpandedDetailRow";
import { expandColumn, useExpandedRows } from "@/hooks/useExpandedRows";
import { cn } from "@/lib/utils";
import type { UpsEvent } from "../types/ups";
import { UPS_EVENTS_LIMIT } from "../api/ups-queries";
import { eventDuration, eventEnded, eventLabel, eventTone, formatRuntime, type UpsTone } from "../lib/ups-state";

type SortKey = "expand" | "when" | "event" | "ups" | "duration";

interface Ctx {
  expanded: Set<string>;
  now: number;
}

const DOT: Record<UpsTone, string> = {
  ok: "bg-emerald-500",
  warn: "bg-amber-500",
  err: "bg-red-500",
  idle: "bg-muted-foreground/50",
};

function columns(showUps: boolean): ColumnDef<UpsEvent, SortKey, Ctx>[] {
  const cols: ColumnDef<UpsEvent, SortKey, Ctx>[] = [
    expandColumn("expand"),
    {
      key: "when",
      label: "When",
      width: 190,
      sortValue: (e) => e.started_at,
      cell: (e) => <span className="text-sm">{formatDateTime(e.started_at)}</span>,
    },
    {
      key: "event",
      label: "Event",
      width: 280,
      sortValue: (e) => eventLabel(e),
      cell: (e) => (
        <span className="flex items-center gap-2 font-medium">
          <span className={cn("h-2 w-2 shrink-0 rounded-full", DOT[eventTone(e)])} aria-hidden="true" />
          {eventLabel(e)}
        </span>
      ),
    },
  ];
  if (showUps) {
    cols.push({
      key: "ups",
      label: "UPS",
      width: 160,
      sortValue: (e) => e.ups_name,
      cell: (e) => (
        <Link to={`/power/${e.ups_id}`} className="hover:underline" onClick={(ev) => { ev.stopPropagation(); }}>
          {e.ups_name}
        </Link>
      ),
    });
  }
  cols.push({
    key: "duration",
    label: "Duration",
    width: 150,
    sortValue: (e) => (e.ended_at === null ? Infinity : Date.parse(e.ended_at) - Date.parse(e.started_at)),
    cell: (e, ctx) => <span className="text-sm text-muted-foreground">{eventDuration(e, ctx.now)}</span>,
  });
  return cols;
}

/** Says a list stops at the newest page the API returned. */
export function LatestEventsNote({ count }: { count: number }) {
  if (count < UPS_EVENTS_LIMIT) return null;
  return (
    <p className="text-xs text-muted-foreground">
      Showing the latest {UPS_EVENTS_LIMIT} power events.
    </p>
  );
}

/**
 * Power events, newest first, each expandable to what Nexara recorded about
 * it. An episode closed after a gap in contact says its end was not seen, so
 * a lost connection is never presented as the moment power came back.
 */
export function PowerEventsTable({ events, showUps = false }: { events: UpsEvent[]; showUps?: boolean }) {
  const cols = useMemo(() => columns(showUps), [showUps]);
  const { layout, rows, toggle, directionFor } = useDataTable(
    showUps ? "ups-events" : "ups-device-events",
    cols,
    events,
    byId,
  );
  const { expanded, toggle: toggleExpand } = useExpandedRows();
  const ctx: Ctx = { expanded, now: Date.now() };

  if (events.length === 0) {
    return <p className="py-6 text-center text-sm text-muted-foreground">No power events recorded yet.</p>;
  }

  return (
    <div className="rounded-md border">
      <DataTableFrame layout={layout}>
        <TableHeader>
          <DataTableHeadRow layout={layout} directionFor={directionFor} onSort={toggle} />
        </TableHeader>
        <TableBody>
          {rows.map((e) => (
            <Fragment key={e.id}>
              <TableRow className="cursor-pointer" onClick={() => { toggleExpand(e.id); }}>
                <DataTableCells row={e} layout={layout} ctx={ctx} />
              </TableRow>
              {expanded.has(e.id) && (
                <ExpandedDetailRow colSpan={layout.columns.length}>
                  <dl className="grid grid-cols-1 gap-x-6 gap-y-2 text-sm sm:grid-cols-2 lg:grid-cols-3">
                    <DetailField label="Started">{formatDateTime(e.started_at)}</DetailField>
                    {e.kind !== "self_test" && (
                      <DetailField label="Ended">
                        {eventEnded(e, e.ended_at === null ? "" : formatDateTime(e.ended_at))}
                      </DetailField>
                    )}
                    <DetailField label="UPS">{e.ups_description !== "" ? `${e.ups_name} (${e.ups_description})` : e.ups_name}</DetailField>
                    <DetailField label="NUT server">{e.server_name}</DetailField>
                    {e.details.lowest_charge !== undefined && (
                      <DetailField label="Lowest charge">{`${String(Math.round(e.details.lowest_charge))}%`}</DetailField>
                    )}
                    {e.details.lowest_runtime !== undefined && (
                      <DetailField label="Lowest runtime">{formatRuntime(e.details.lowest_runtime)}</DetailField>
                    )}
                    {e.details.peak_load !== undefined && (
                      <DetailField label="Peak load">{`${String(Math.round(e.details.peak_load))}%`}</DetailField>
                    )}
                    {e.details.status !== undefined && e.details.status !== "" && (
                      <DetailField label="Status" variant="mono">{e.details.status}</DetailField>
                    )}
                    {e.details.reason !== undefined && <DetailField label="Reason">{e.details.reason}</DetailField>}
                    {e.details.result !== undefined && <DetailField label="Result">{e.details.result}</DetailField>}
                  </dl>
                </ExpandedDetailRow>
              )}
            </Fragment>
          ))}
        </TableBody>
      </DataTableFrame>
    </div>
  );
}
