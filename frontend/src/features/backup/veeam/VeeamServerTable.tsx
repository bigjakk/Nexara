import { Fragment, useState } from "react";
import { TableBody, TableHeader, TableRow } from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Pencil, PlugZap, Trash2 } from "lucide-react";
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
import { useTestVeeamServer } from "../api/backup-queries";
import type { VeeamProbeResult, VeeamServer } from "../types/backup";
import { VeeamProbeSummary } from "./VeeamProbeSummary";
import { VeeamServerStatusBadge } from "./VeeamServerStatusBadge";
import {
  veeamServerStatus,
  type VeeamServerStatus,
} from "./veeam-server-status";

interface VeeamServerTableProps {
  servers: VeeamServer[];
  onEdit: (server: VeeamServer) => void;
  onDelete: (server: VeeamServer) => void;
}

const REQUIRED_EDITION = "EnterprisePlus";

type ServerSortKey =
  | "expand"
  | "name"
  | "address"
  | "version"
  | "edition"
  | "status"
  | "actions";

/** What the chevron and action cells need beyond the server row itself. */
interface ServerCtx {
  expanded: Set<string>;
  testingId: string | null;
  onTest: (server: VeeamServer) => void;
  onEdit: (server: VeeamServer) => void;
  onDelete: (server: VeeamServer) => void;
}

/**
 * Severity order for the Status column, not alphabetical.
 *
 * Sorted by label, ascending gives Connected, Disabled, Error, Syncing — so
 * the first click on Status buries the broken servers at the bottom, which is
 * the opposite of why anyone clicks it. The cell still shows the badge's
 * label; only the ordering is ranked.
 */
const STATUS_RANK: Record<VeeamServerStatus, number> = {
  Error: 0,
  Disabled: 1,
  Syncing: 2,
  Connected: 3,
};

/** Each column sorts on what its cell SHOWS, not on the underlying field. */
const COLUMNS: ColumnDef<VeeamServer, ServerSortKey, ServerCtx>[] = [
  expandColumn("expand"),
  {
    key: "name",
    label: "Name",
    width: 180,
    sortValue: (server) => server.name,
    cell: (server) => <span className="font-medium">{server.name}</span>,
  },
  {
    key: "address",
    label: "Address",
    width: 260,
    sortValue: (server) => server.base_url,
    // A Veeam base URL is an FQDN with a port; clipping it hides which host a
    // row is even about.
    wrap: true,
    cell: (server) => (
      <span className="font-mono text-xs break-all">{server.base_url}</span>
    ),
  },
  {
    key: "version",
    label: "Version",
    width: 140,
    sortValue: (server) => server.product_version || null,
    cell: (server) => (
      <span className="font-mono text-sm">{server.product_version || "-"}</span>
    ),
  },
  {
    key: "edition",
    label: "Edition",
    width: 150,
    sortValue: (server) => server.license_edition || null,
    cell: (server) =>
      server.license_edition === "" ? (
        <span className="text-muted-foreground">-</span>
      ) : (
        <Badge
          variant={
            server.license_edition === REQUIRED_EDITION ? "default" : "secondary"
          }
        >
          {server.license_edition}
        </Badge>
      ),
  },
  {
    key: "status",
    label: "Status",
    width: 130,
    sortValue: (server) => STATUS_RANK[veeamServerStatus(server)],
    cell: (server) => <VeeamServerStatusBadge server={server} />,
  },
  {
    key: "actions",
    label: "Actions",
    width: 140,
    align: "right",
    fixed: true,
    // The row toggles expansion on click, so the buttons stop the event
    // themselves — otherwise testing a connection would also expand the row.
    cell: (server, ctx) => (
      <div
        onClick={(e) => {
          e.stopPropagation();
        }}
      >
        <Button
          variant="ghost"
          size="sm"
          title="Test connection"
          disabled={ctx.testingId === server.id}
          onClick={() => {
            ctx.onTest(server);
          }}
        >
          <PlugZap className="h-4 w-4" />
        </Button>
        <Button
          variant="ghost"
          size="sm"
          title="Edit"
          onClick={() => {
            ctx.onEdit(server);
          }}
        >
          <Pencil className="h-4 w-4" />
        </Button>
        <Button
          variant="ghost"
          size="sm"
          title="Delete"
          onClick={() => {
            ctx.onDelete(server);
          }}
        >
          <Trash2 className="h-4 w-4" />
        </Button>
      </div>
    ),
  },
];

function formatTimestamp(value: string | null): string {
  if (value == null || value === "") return "Never";
  const parsed = new Date(value);
  if (Number.isNaN(parsed.getTime())) return value;
  return parsed.toLocaleString();
}

export function VeeamServerTable({
  servers,
  onEdit,
  onDelete,
}: VeeamServerTableProps) {
  const {
    layout,
    rows: sortedServers,
    toggle: toggleSort,
    directionFor,
  } = useDataTable("veeam-servers", COLUMNS, servers, byId);

  const { expanded, toggle: toggleExpand, expand } = useExpandedRows();
  // Per-server test results, keyed by id: one server's probe must not clear
  // another's, and a shared mutation result would do exactly that.
  const [probes, setProbes] = useState<Record<string, VeeamProbeResult>>({});
  const [probeErrors, setProbeErrors] = useState<Record<string, string>>({});
  const [testingId, setTestingId] = useState<string | null>(null);

  const testServer = useTestVeeamServer();

  const cellCtx: ServerCtx = {
    expanded,
    testingId,
    onTest: handleTest,
    onEdit,
    onDelete,
  };

  function handleTest(server: VeeamServer) {
    setTestingId(server.id);
    // Drop any error from a previous attempt on this server, without
    // disturbing the others.
    setProbeErrors((prev) =>
      Object.fromEntries(
        Object.entries(prev).filter(([id]) => id !== server.id),
      ),
    );

    testServer.mutate(server.id, {
      onSuccess: (result) => {
        setProbes((prev) => ({ ...prev, [server.id]: result }));
        // Show the answer without making the operator hunt for it.
        expand(server.id);
      },
      onError: (err) => {
        setProbeErrors((prev) => ({
          ...prev,
          [server.id]:
            err instanceof Error ? err.message : "Connection test failed",
        }));
        expand(server.id);
      },
      onSettled: () => {
        setTestingId(null);
      },
    });
  }

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
          {sortedServers.map((server) => {
            const isExpanded = expanded.has(server.id);
            const probe = probes[server.id];
            const probeError = probeErrors[server.id];

            return (
              <Fragment key={server.id}>
                <TableRow
                  className="cursor-pointer"
                  onClick={() => {
                    toggleExpand(server.id);
                  }}
                >
                  <DataTableCells row={server} layout={layout} ctx={cellCtx} />
                </TableRow>

                {isExpanded && (
                  <ExpandedDetailRow
                    colSpan={layout.columns.length}
                    spacing="space-y-4"
                  >
                    <dl className="grid gap-x-6 gap-y-2 text-sm sm:grid-cols-2 lg:grid-cols-3">
                      {/* Withheld from read-only callers: it is half of a
                          domain administrator credential. */}
                      <DetailField
                        label="Username"
                        variant={server.username === "" ? "muted" : "mono"}
                      >
                        {server.username === ""
                          ? "Hidden — requires manage:veeam"
                          : server.username}
                      </DetailField>
                      <DetailField label="API revision" variant="mono">
                        {server.api_revision || "-"}
                      </DetailField>
                      <DetailField label="Certificate">
                        {server.tls_fingerprint !== ""
                          ? "Pinned (SHA-256)"
                          : server.verify_tls
                            ? "Verified against system CAs"
                            : "Not verified"}
                      </DetailField>
                      <DetailField label="Last sync">
                        {formatTimestamp(server.last_sync_at)}
                      </DetailField>
                      <DetailField label="Added">
                        {formatTimestamp(server.created_at)}
                      </DetailField>
                    </dl>

                    {server.tls_fingerprint !== "" && (
                      <div>
                        <p className="mb-1 text-xs text-muted-foreground">
                          Pinned fingerprint
                        </p>
                        <code className="select-all break-all font-mono text-xs">
                          {server.tls_fingerprint}
                        </code>
                      </div>
                    )}

                    {server.last_sync_error !== "" && (
                      <p className="text-sm text-destructive">
                        Last sync failed: {server.last_sync_error}
                      </p>
                    )}

                    {probeError != null && (
                      <p className="text-sm text-destructive">{probeError}</p>
                    )}

                    {probe != null && probeError == null && (
                      <VeeamProbeSummary probe={probe} />
                    )}
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
