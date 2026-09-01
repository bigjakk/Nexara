import { Fragment, useState } from "react";
import {
  Table,
  TableBody,
  TableCell,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  ChevronDown,
  ChevronRight,
  Pencil,
  PlugZap,
  Trash2,
} from "lucide-react";
import { byId, useTableSort } from "@/hooks/useTableSort";
import {
  sortAccessorsFrom,
  useColumnLayout,
  type ColumnDef,
} from "@/hooks/useColumnLayout";
import { DataTableHead } from "@/components/DataTableHead";
import { DataTableCells } from "@/components/DataTableCells";
import { ResetColumnsButton } from "@/components/ResetColumnsButton";
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
  {
    key: "expand",
    label: "",
    width: 40,
    fixed: true,
    cell: (server, ctx) =>
      ctx.expanded.has(server.id) ? (
        <ChevronDown className="h-4 w-4" />
      ) : (
        <ChevronRight className="h-4 w-4" />
      ),
  },
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

const SERVER_SORT = sortAccessorsFrom(COLUMNS);

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
    rows: sortedServers,
    toggle: toggleSort,
    directionFor,
  } = useTableSort(servers, SERVER_SORT, byId);
  const layout = useColumnLayout("veeam-servers", COLUMNS);

  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  // Per-server test results, keyed by id: one server's probe must not clear
  // another's, and a shared mutation result would do exactly that.
  const [probes, setProbes] = useState<Record<string, VeeamProbeResult>>({});
  const [probeErrors, setProbeErrors] = useState<Record<string, string>>({});
  const [testingId, setTestingId] = useState<string | null>(null);

  const testServer = useTestVeeamServer();

  function toggleExpand(id: string) {
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(id)) {
        next.delete(id);
      } else {
        next.add(id);
      }
      return next;
    });
  }

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
        setExpanded((prev) => new Set(prev).add(server.id));
      },
      onError: (err) => {
        setProbeErrors((prev) => ({
          ...prev,
          [server.id]:
            err instanceof Error ? err.message : "Connection test failed",
        }));
        setExpanded((prev) => new Set(prev).add(server.id));
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
      <div className="overflow-x-auto">
        <Table className="table-fixed" style={{ width: layout.totalWidth }}>
          <TableHeader>
            <TableRow>
              {layout.columns.map((col) => (
                <DataTableHead
                  key={col.key}
                  column={col}
                  layout={layout}
                  direction={directionFor(col.key)}
                  onSort={() => {
                    toggleSort(col.key);
                  }}
                />
              ))}
            </TableRow>
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
                    <DataTableCells
                      row={server}
                      layout={layout}
                      ctx={cellCtx}
                    />
                  </TableRow>

                  {isExpanded && (
                    <TableRow>
                      <TableCell
                        colSpan={layout.columns.length}
                        className="bg-muted/30"
                      >
                        <div className="space-y-4 px-2 py-3">
                          <dl className="grid gap-x-6 gap-y-2 text-sm sm:grid-cols-2 lg:grid-cols-3">
                            <div>
                              <dt className="text-xs text-muted-foreground">
                                Username
                              </dt>
                              {/* Withheld from read-only callers: it is half
                                  of a domain administrator credential. */}
                              <dd
                                className={
                                  server.username === ""
                                    ? "text-muted-foreground"
                                    : "font-mono"
                                }
                              >
                                {server.username === ""
                                  ? "Hidden — requires manage:veeam"
                                  : server.username}
                              </dd>
                            </div>
                            <div>
                              <dt className="text-xs text-muted-foreground">
                                API revision
                              </dt>
                              <dd className="font-mono">
                                {server.api_revision || "-"}
                              </dd>
                            </div>
                            <div>
                              <dt className="text-xs text-muted-foreground">
                                Certificate
                              </dt>
                              <dd>
                                {server.tls_fingerprint !== ""
                                  ? "Pinned (SHA-256)"
                                  : server.verify_tls
                                    ? "Verified against system CAs"
                                    : "Not verified"}
                              </dd>
                            </div>
                            <div>
                              <dt className="text-xs text-muted-foreground">
                                Last sync
                              </dt>
                              <dd>{formatTimestamp(server.last_sync_at)}</dd>
                            </div>
                            <div>
                              <dt className="text-xs text-muted-foreground">
                                Added
                              </dt>
                              <dd>{formatTimestamp(server.created_at)}</dd>
                            </div>
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
                            <p className="text-sm text-destructive">
                              {probeError}
                            </p>
                          )}

                          {probe != null && probeError == null && (
                            <VeeamProbeSummary probe={probe} />
                          )}
                        </div>
                      </TableCell>
                    </TableRow>
                  )}
                </Fragment>
              );
            })}
          </TableBody>
        </Table>
      </div>
    </div>
  );
}
