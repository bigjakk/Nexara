import { Fragment, useState } from "react";
import { Pencil, PlugZap, Trash2 } from "lucide-react";
import { TableBody, TableHeader, TableRow } from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { formatDateTime, formatRelativeTime } from "@/lib/format";
import { byId } from "@/hooks/useTableSort";
import type { ColumnDef } from "@/hooks/useColumnLayout";
import { useDataTable } from "@/hooks/useDataTable";
import { DataTableFrame } from "@/components/DataTableFrame";
import { DataTableHeadRow } from "@/components/DataTableHeadCells";
import { DataTableCells } from "@/components/DataTableCells";
import { DetailField } from "@/components/DetailField";
import { ExpandedDetailRow } from "@/components/ExpandedDetailRow";
import { expandColumn, useExpandedRows } from "@/hooks/useExpandedRows";
import { describeError } from "@/lib/api-error";
import { useTestNutServer } from "../api/ups-queries";
import type { NutProbeResult, NutServer } from "../types/ups";

type SortKey = "expand" | "name" | "address" | "version" | "access" | "status" | "actions";

interface Ctx {
  expanded: Set<string>;
  canManage: boolean;
  canDelete: boolean;
  onTestResult: (id: string, result: NutProbeResult | string) => void;
  onEdit: (s: NutServer) => void;
  onDelete: (s: NutServer) => void;
}

function serverStatus(s: NutServer): { label: string; rank: number; variant: "default" | "secondary" | "destructive" } {
  if (!s.enabled) return { label: "Disabled", rank: 1, variant: "secondary" };
  if (s.last_poll_error !== "") return { label: "Failing", rank: 0, variant: "destructive" };
  if (s.last_poll_at === null) return { label: "Waiting", rank: 2, variant: "secondary" };
  return { label: "Connected", rank: 3, variant: "default" };
}

/** Its own mutation per row, so two rows tested at once each keep their result. */
function TestButton({ server, onResult }: { server: NutServer; onResult: Ctx["onTestResult"] }) {
  const test = useTestNutServer();
  return (
    <Button
      variant="ghost"
      size="sm"
      title="Test connection"
      disabled={test.isPending}
      onClick={() => {
        test.mutate(server.id, {
          onSuccess: (r) => { onResult(server.id, r); },
          onError: (e) => { onResult(server.id, describeError(e) || "the request did not reach Nexara"); },
        });
      }}
    >
      <PlugZap className="h-4 w-4" />
    </Button>
  );
}

const COLUMNS: ColumnDef<NutServer, SortKey, Ctx>[] = [
  expandColumn("expand"),
  { key: "name", label: "Name", width: 160, sortValue: (s) => s.name, cell: (s) => <span className="font-medium">{s.name}</span> },
  {
    key: "address",
    label: "Address",
    width: 200,
    sortValue: (s) => `${s.host}:${String(s.port)}`,
    cell: (s) => <span className="font-mono text-xs">{s.host}:{s.port}</span>,
  },
  {
    key: "version",
    label: "NUT",
    width: 150,
    sortValue: (s) => s.nut_version || null,
    cell: (s) => (
      <span className="text-sm">
        {s.nut_version || "?"}
        {s.protocol_version !== "" && <span className="text-muted-foreground"> · protocol {s.protocol_version}</span>}
      </span>
    ),
  },
  {
    key: "access",
    label: "Access",
    width: 170,
    sortValue: (s) => (s.has_password ? 1 : 0),
    cell: (s) => (
      <span className="text-sm">
        {s.has_password ? "With credentials" : "Anonymous"} · {s.tls_mode === "starttls" ? "STARTTLS" : "no TLS"}
      </span>
    ),
  },
  {
    key: "status",
    label: "Status",
    width: 170,
    sortValue: (s) => serverStatus(s).rank,
    cell: (s) => {
      const st = serverStatus(s);
      return (
        <div className="flex items-center gap-2">
          <Badge variant={st.variant} className={st.label === "Connected" ? "bg-emerald-600" : undefined}>{st.label}</Badge>
          {s.last_poll_at !== null && <span className="text-xs text-muted-foreground">{formatRelativeTime(s.last_poll_at)}</span>}
        </div>
      );
    },
  },
  {
    key: "actions",
    label: "Actions",
    width: 150,
    align: "right",
    fixed: true,
    cell: (s, ctx) => (
      <div onClick={(e) => { e.stopPropagation(); }}>
        {ctx.canManage && <TestButton server={s} onResult={ctx.onTestResult} />}
        {ctx.canManage && (
          <Button variant="ghost" size="sm" title="Edit" onClick={() => { ctx.onEdit(s); }}>
            <Pencil className="h-4 w-4" />
          </Button>
        )}
        {ctx.canDelete && (
          <Button variant="ghost" size="sm" title="Remove" onClick={() => { ctx.onDelete(s); }}>
            <Trash2 className="h-4 w-4" />
          </Button>
        )}
      </div>
    ),
  },
];

export function NutServerTable({
  servers,
  canManage,
  canDelete,
  onEdit,
  onDelete,
}: {
  servers: NutServer[];
  canManage: boolean;
  canDelete: boolean;
  onEdit: (s: NutServer) => void;
  onDelete: (s: NutServer) => void;
}) {
  const { layout, rows, toggle, directionFor } = useDataTable("nut-servers", COLUMNS, servers, byId);
  const { expanded, toggle: toggleExpand, expand } = useExpandedRows();
  const [tests, setTests] = useState<Record<string, NutProbeResult | string>>({});
  const ctx: Ctx = {
    expanded,
    canManage,
    canDelete,
    onEdit,
    onDelete,
    onTestResult: (id, r) => {
      setTests((prev) => ({ ...prev, [id]: r }));
      expand(id);
    },
  };

  return (
    <div className="rounded-md border">
      <DataTableFrame layout={layout}>
        <TableHeader>
          <DataTableHeadRow layout={layout} directionFor={directionFor} onSort={toggle} />
        </TableHeader>
        <TableBody>
          {rows.map((s) => {
            const test = tests[s.id];
            return (
              <Fragment key={s.id}>
                <TableRow className="cursor-pointer" onClick={() => { toggleExpand(s.id); }}>
                  <DataTableCells row={s} layout={layout} ctx={ctx} />
                </TableRow>
                {expanded.has(s.id) && (
                  <ExpandedDetailRow colSpan={layout.columns.length} spacing="space-y-3">
                    <dl className="grid grid-cols-1 gap-x-6 gap-y-2 text-sm sm:grid-cols-2 lg:grid-cols-3">
                      <DetailField label="Username" variant={s.username === "" ? "muted" : "mono"}>
                        {s.username !== "" ? s.username : s.has_password ? "Hidden — requires manage:ups" : "None"}
                      </DetailField>
                      <DetailField label="Certificate">
                        {s.tls_mode !== "starttls"
                          ? "No TLS"
                          : s.tls_fingerprint !== ""
                            ? "Pinned (SHA-256)"
                            : s.verify_tls
                              ? "Verified against system CAs"
                              : "Not verified"}
                      </DetailField>
                      <DetailField label="Last successful poll">{formatDateTime(s.last_poll_at, "Never")}</DetailField>
                      <DetailField label="Added">{formatDateTime(s.created_at)}</DetailField>
                    </dl>
                    {s.last_poll_error !== "" && (
                      <p className="text-sm text-destructive">Last poll failed: {s.last_poll_error}</p>
                    )}
                    {typeof test === "string" && <p className="text-sm text-destructive">Test failed: {test}</p>}
                    {test !== undefined && typeof test !== "string" && (
                      <p className="text-sm">
                        Test passed in {test.took_ms} ms: {test.devices.length} UPS (
                        {test.devices.map((d) => d.name).join(", ")})
                      </p>
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
