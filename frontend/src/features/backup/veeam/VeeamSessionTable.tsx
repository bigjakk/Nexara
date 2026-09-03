import { Fragment } from "react";
import { TableBody, TableHeader, TableRow } from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";
import { formatBytes, formatDateTime } from "@/lib/format";
import { byId } from "@/hooks/useTableSort";
import type { ColumnDef } from "@/hooks/useColumnLayout";
import { useDataTable } from "@/hooks/useDataTable";
import { DataTableFrame } from "@/components/DataTableFrame";
import { DataTableHeadRow } from "@/components/DataTableHeadCells";
import { DataTableCells } from "@/components/DataTableCells";
import { DetailField } from "@/components/DetailField";
import { ExpandedDetailRow } from "@/components/ExpandedDetailRow";
import { ResetColumnsButton } from "@/components/ResetColumnsButton";
import { expandColumn, useExpandedRows } from "@/hooks/useExpandedRows";
import { veeamDurationSeconds } from "./veeam-duration";
import {
  bottleneckLabel,
  processingRateLabel,
  resultVariant,
} from "./veeam-format";
import { VeeamSessionActions } from "./VeeamSessionActions";
import { VeeamSessionLog } from "./VeeamSessionLog";
import { VeeamTaskTable } from "./VeeamTaskTable";
import type { VeeamSession } from "../types/backup";

interface VeeamSessionTableProps {
  sessions: VeeamSession[];
  /** The server these runs belong to. Stopping one posts against it. */
  serverId: string;
}

/** Epoch ms, so date columns order chronologically rather than by their text. */
function toEpoch(value: string | null): number | null {
  if (value == null || value === "") return null;
  const parsed = new Date(value).getTime();
  return Number.isNaN(parsed) ? null : parsed;
}

/**
 * Severity order for the Result column, not alphabetical.
 *
 * Sorting the label ascending gives Failed, Stopped, Success, Warning — which
 * is close to right by accident, and would silently stop being right if a
 * label were reworded. Ranking says it on purpose. The cell still shows the
 * label; only the ordering is domain-ranked.
 */
const SESSION_RESULT_RANK: Record<string, number> = {
  "Failed or cancelled": 0,
  "Stopped from Nexara": 1,
  Warning: 2,
  Success: 3,
};

/**
 * The Result cell's own label.
 *
 * Named for sessions specifically: VeeamJobTable has its own `resultLabel`
 * taking a bare result string, and a job's last_result carries no
 * nexara_stopped flag to consult. Merging them would silently drop the
 * distinction between an operator's stop and a genuine backup failure.
 */
function sessionResultLabel(session: VeeamSession): string | null {
  if (session.result === "") return null;
  if (session.result !== "Failed") return session.result;
  return session.nexara_stopped ? "Stopped from Nexara" : "Failed or cancelled";
}

type SessionSortKey =
  | "expand"
  | "name"
  | "state"
  | "result"
  | "mode"
  | "started"
  | "duration"
  | "transferred"
  | "actions";

/** What the chevron and action cells need beyond the session row itself. */
interface SessionCtx {
  serverId: string;
  expanded: Set<string>;
}

/** Each column sorts on what its cell SHOWS, not on the underlying field. */
const COLUMNS: ColumnDef<VeeamSession, SessionSortKey, SessionCtx>[] = [
  expandColumn("expand"),
  {
    key: "name",
    label: "Job",
    width: 220,
    sortValue: (session) => session.name,
    cell: (session) => <span className="font-medium">{session.name}</span>,
  },
  {
    key: "state",
    label: "State",
    width: 130,
    sortValue: (session) => session.state || null,
    cell: (session) => (
      <Badge variant="secondary">{session.state || "-"}</Badge>
    ),
  },
  {
    key: "result",
    label: "Result",
    width: 190,
    sortValue: (session) => {
      const label = sessionResultLabel(session);
      return label === null ? null : (SESSION_RESULT_RANK[label] ?? 99);
    },
    cell: (session) =>
      session.result === "" ? (
        <span className="text-muted-foreground">—</span>
      ) : (
        <Badge variant={resultVariant(session.result)}>
          {/* Never a bare "Failed": Veeam records an API-cancelled run
              identically to a real failure. Keyed on nexara_STOPPED, not
              nexara_initiated — a run Nexara STARTED can fail for a completely
              real reason, and labelling that "stopped" would tell an operator
              to ignore a genuine backup failure. */}
          {sessionResultLabel(session)}
        </Badge>
      ),
  },
  {
    key: "mode",
    label: "Mode",
    width: 130,
    sortValue: (session) => session.algorithm || null,
    cell: (session) => (
      <span className="text-sm">{session.algorithm || "—"}</span>
    ),
  },
  {
    key: "started",
    label: "Started",
    width: 180,
    sortValue: (session) => toEpoch(session.creation_time),
    cell: (session) => (
      <span className="text-sm">{formatDateTime(session.creation_time)}</span>
    ),
  },
  {
    key: "duration",
    label: "Duration",
    width: 120,
    sortValue: (session) =>
      session.duration === "" ? null : veeamDurationSeconds(session.duration),
    cell: (session) => (
      <span className="font-mono text-sm">{session.duration || "—"}</span>
    ),
  },
  {
    key: "transferred",
    label: "Transferred",
    width: 130,
    align: "right",
    sortValue: (session) => session.transferred_size,
    cell: (session) => (
      <span className="font-mono text-sm">
        {formatBytes(session.transferred_size)}
      </span>
    ),
  },
  {
    key: "actions",
    label: "Actions",
    width: 230,
    align: "right",
    fixed: true,
    // The action components render their mutation errors and notices inline
    // beside the buttons; truncating this cell would clip the only feedback a
    // failed start/stop ever gives.
    wrap: true,
    cell: (session, ctx) => (
      <VeeamSessionActions serverId={ctx.serverId} session={session} />
    ),
  },
];

export function VeeamSessionTable({
  sessions,
  serverId,
}: VeeamSessionTableProps) {
  const {
    layout,
    rows: sortedSessions,
    toggle: toggleSort,
    directionFor,
  } = useDataTable("veeam-sessions", COLUMNS, sessions, byId);

  const { expanded, toggle: toggleExpand } = useExpandedRows(serverId);

  if (sessions.length === 0) {
    return (
      <p className="py-8 text-center text-sm text-muted-foreground">
        No Proxmox backup runs recorded yet.
      </p>
    );
  }

  const cellCtx: SessionCtx = { serverId, expanded };

  return (
    <div className="rounded-md border">
      <div className="flex justify-end px-2 pt-2">
        <ResetColumnsButton layout={layout} />
      </div>
      <DataTableFrame layout={layout}>
        <TableHeader>
          <DataTableHeadRow
            layout={layout}
            directionFor={directionFor}
            onSort={toggleSort}
          />
        </TableHeader>
        <TableBody>
          {sortedSessions.map((session) => {
            const isExpanded = expanded.has(session.id);

            return (
              <Fragment key={session.id}>
                <TableRow
                  className="cursor-pointer"
                  onClick={() => {
                    toggleExpand(session.id);
                  }}
                >
                  <DataTableCells row={session} layout={layout} ctx={cellCtx} />
                </TableRow>

                {isExpanded && (
                  <ExpandedDetailRow colSpan={layout.columns.length}>
                    <dl className="grid gap-x-6 gap-y-2 text-sm sm:grid-cols-2 lg:grid-cols-4">
                      <DetailField label="Finished">
                        {formatDateTime(session.end_time)}
                      </DetailField>
                      <DetailField label="Bottleneck">
                        {bottleneckLabel(session.bottleneck)}
                      </DetailField>
                      <DetailField label="Processing rate" variant="mono">
                        {processingRateLabel(session.processing_rate)}
                      </DetailField>
                      <DetailField label="Initiated by">
                        {session.initiated_by || "—"}
                      </DetailField>
                      <DetailField label="Processed" variant="mono">
                        {formatBytes(session.processed_size)}
                      </DetailField>
                      <DetailField label="Read" variant="mono">
                        {formatBytes(session.read_size)}
                      </DetailField>
                    </dl>

                    {session.result_message !== "" &&
                      session.result_message !== "Success" && (
                        <p className="text-sm text-muted-foreground">
                          {session.result_message}
                        </p>
                      )}

                    {session.result === "Failed" && !session.nexara_stopped && (
                      <p className="text-xs text-muted-foreground">
                        Veeam records a run stopped through its API the same way
                        it records a genuine failure, with no cancellation flag
                        and an empty log — so this may have been someone
                        stopping the job from the Veeam console.
                      </p>
                    )}

                    {/* Which guests this run processed, and which failed.
                        The run's own result cannot say — "Failed" on a job
                        covering eleven guests names none of them. */}
                    <VeeamTaskTable
                      serverId={serverId}
                      sessionVeeamId={session.veeam_id}
                      enabled={isExpanded}
                      // An empty state is FINISHED, not running —
                      // matching VeeamSessionActions, which reads the
                      // same field and already treats it that way.
                      // Without the first clause a malformed row tells
                      // the operator to wait for detail on a run that is
                      // not going anywhere.
                      running={
                        session.state !== "" && session.state !== "Stopped"
                      }
                    />

                    {/* Fetched only once the row is open: each read costs a
                        fresh logon against the Veeam server. */}
                    <VeeamSessionLog
                      serverId={serverId}
                      session={session}
                      enabled={isExpanded}
                    />
                  </ExpandedDetailRow>
                )}
              </Fragment>
            );
          })}
        </TableBody>
      </DataTableFrame>
    </div>
  );
}
