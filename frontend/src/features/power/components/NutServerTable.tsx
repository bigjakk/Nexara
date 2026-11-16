import { Fragment, useState } from "react";
import { Pencil, PlugZap, Trash2 } from "lucide-react";
import { TableBody, TableHeader, TableRow } from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { byId } from "@/hooks/useTableSort";
import type { ColumnDef } from "@/hooks/useColumnLayout";
import { useDataTable } from "@/hooks/useDataTable";
import { DataTableFrame } from "@/components/DataTableFrame";
import { DataTableHeadRow } from "@/components/DataTableHeadCells";
import { DataTableCells } from "@/components/DataTableCells";
import { DetailField } from "@/components/DetailField";
import { ExpandedDetailRow } from "@/components/ExpandedDetailRow";
import { DefGrid } from "@/components/page/Panel";
import { expandColumn, useExpandedRows } from "@/hooks/useExpandedRows";
import { describeError } from "@/lib/api-error";
import { useSaveOutcome } from "@/hooks/useSaveOutcome";
import { useTestNutServer } from "../api/ups-queries";
import type { NutProbeResult, NutServer } from "../types/ups";
import { DateTime } from "@/components/DateTime";
import { useTranslation } from "react-i18next";
import i18n from "@/lib/i18n";

type SortKey = "expand" | "name" | "address" | "version" | "access" | "status" | "actions";

interface Ctx {
  expanded: Set<string>;
  canManage: boolean;
  canDelete: boolean;
  onTestResult: (id: string, result: NutProbeResult | string) => void;
  onEdit: (s: NutServer) => void;
  onDelete: (s: NutServer) => void;
}

function serverStatus(s: NutServer): {
  label: string;
  rank: number;
  variant: "healthy" | "secondary" | "warning" | "destructive";
} {
  if (!s.enabled) return { label: i18n.t("power:nutServerTable.disabled"), rank: 2, variant: "secondary" };
  if (s.last_poll_error !== "") return { label: i18n.t("power:nutServerTable.failing"), rank: 0, variant: "destructive" };
  if (s.last_poll_at === null) return { label: i18n.t("power:nutServerTable.waiting"), rank: 3, variant: "secondary" };
  // A success the poller has not repeated lately is not a live connection.
  if (!s.connected) return { label: i18n.t("power:nutServerTable.notPolledLately"), rank: 1, variant: "warning" };
  return { label: i18n.t("power:nutServerTable.connected"), rank: 4, variant: "healthy" };
}

/**
 * Its own mutation per row, so two rows tested at once each keep their result.
 * A failure that lands once the row has gone (the page left) is toasted, once:
 * mutate()'s own onError is never run then (useSaveOutcome).
 */
function TestButton({ server, onResult }: { server: NutServer; onResult: Ctx["onTestResult"] }) {
  const { t } = useTranslation("power");
  const test = useTestNutServer();
  const settle = useSaveOutcome();
  return (
    <Button
      variant="ghost"
      size="sm"
      title={t("nutServerTable.testConnection")}
      disabled={test.isPending}
      onClick={() => {
        settle(test.mutateAsync(server.id), {
          action: t("nutServerTable.testingNUTServerName", { name: server.name }),
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
  { key: "name", get label() { return i18n.t("power:nutServerTable.name"); }, width: 160, sortValue: (s) => s.name, cell: (s) => <span className="font-medium">{s.name}</span> },
  {
    key: "address",
    get label() { return i18n.t("power:nutServerTable.address"); },
    width: 200,
    sortValue: (s) => `${s.host}:${String(s.port)}`,
    cell: (s) => <span className="font-mono text-xs">{s.host}:{s.port}</span>,
  },
  {
    key: "version",
    get label() { return i18n.t("power:nutServerTable.nut"); },
    width: 150,
    sortValue: (s) => s.nut_version || null,
    cell: (s) => (
      <span className="text-sm">
        {s.nut_version || "?"}
        {s.protocol_version !== "" && <span className="text-muted-foreground"> {i18n.t("power:nutServerTable.protocolProtocolVersion", { protocolVersion: s.protocol_version })}</span>}
      </span>
    ),
  },
  {
    key: "access",
    get label() { return i18n.t("power:nutServerTable.access"); },
    width: 170,
    sortValue: (s) => (s.commands_verified_at !== null ? 2 : s.has_password ? 1 : 0),
    cell: (s) => (
      <span className="text-sm">
        {s.commands_verified_at !== null ? i18n.t("power:nutServerTable.commands") : s.has_password ? i18n.t("power:nutServerTable.withCredentials") : i18n.t("power:nutServerTable.anonymous")} ·{" "}
        {s.tls_mode === "starttls" ? i18n.t("power:nutServerTable.starttls") : i18n.t("power:nutServerTable.noTLS")}
      </span>
    ),
  },
  {
    key: "status",
    get label() { return i18n.t("power:nutServerTable.status"); },
    width: 170,
    sortValue: (s) => serverStatus(s).rank,
    cell: (s) => {
      const st = serverStatus(s);
      return (
        <div className="flex items-center gap-2">
          <Badge variant={st.variant}>{st.label}</Badge>
          {s.last_poll_at !== null && <span className="text-xs text-muted-foreground"><DateTime value={s.last_poll_at} auto="relative" /></span>}
        </div>
      );
    },
  },
  {
    key: "actions",
    get label() { return i18n.t("power:nutServerTable.actions"); },
    width: 150,
    align: "right",
    fixed: true,
    cell: (s, ctx) => (
      <div onClick={(e) => { e.stopPropagation(); }}>
        {ctx.canManage && <TestButton server={s} onResult={ctx.onTestResult} />}
        {ctx.canManage && (
          <Button variant="ghost" size="sm" title={i18n.t("power:nutServerTable.edit")} onClick={() => { ctx.onEdit(s); }}>
            <Pencil className="h-4 w-4" />
          </Button>
        )}
        {ctx.canDelete && (
          <Button variant="ghost" size="sm" title={i18n.t("power:nutServerTable.remove")} onClick={() => { ctx.onDelete(s); }}>
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
  const { t } = useTranslation("power");
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
                  <DefGrid>
                    <DetailField label={t("nutServerTable.username")} variant={s.username === "" ? "muted" : "mono"}>
                      {s.username !== "" ? s.username : s.has_password ? t("nutServerTable.hiddenRequiresManageUps") : t("nutServerTable.none")}
                    </DetailField>
                    <DetailField label={t("nutServerTable.certificate")}>
                      {s.tls_mode !== "starttls"
                        ? t("nutServerTable.noTLS2")
                        : s.tls_fingerprint !== ""
                          ? t("nutServerTable.pinnedSHA256")
                          : s.verify_tls
                            ? t("nutServerTable.verifiedAgainstSystemCAs")
                            : t("nutServerTable.notVerified")}
                    </DetailField>
                    <DetailField label={t("nutServerTable.lastSuccessfulPoll")}><DateTime value={s.last_poll_at} fallback={t("nutServerTable.never")} /></DetailField>
                    <DetailField label={t("nutServerTable.added")}><DateTime value={s.created_at} /></DetailField>
                  </DefGrid>
                  {s.last_poll_error !== "" && (
                    <p className="text-sm text-destructive">{t("nutServerTable.lastPollFailedLastPollError", { lastPollError: s.last_poll_error })}</p>
                  )}
                  {typeof test === "string" && <p className="text-sm text-destructive">{t("nutServerTable.testFailedTest", { test })}</p>}
                  {test !== undefined && typeof test !== "string" && (
                    <p className="text-sm">
                      {t("nutServerTable.testPassedInTookMsMs", { tookMs: test.took_ms, length: test.devices.length, devices: test.devices.map((d) => d.name).join(", ") })}
                    </p>
                  )}
                </ExpandedDetailRow>
              )}
            </Fragment>
          );
        })}
      </TableBody>
    </DataTableFrame>
  );
}
