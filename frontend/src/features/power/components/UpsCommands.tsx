import { Fragment, useState } from "react";
import { useQueries } from "@tanstack/react-query";
import { AlertTriangle } from "lucide-react";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { TableBody, TableHeader, TableRow } from "@/components/ui/table";
import { TypeToConfirmDialog } from "@/components/TypeToConfirmDialog";
import { DataTableFrame } from "@/components/DataTableFrame";
import { DataTableHeadRow } from "@/components/DataTableHeadCells";
import { DataTableCells } from "@/components/DataTableCells";
import { ExpandedDetailRow } from "@/components/ExpandedDetailRow";
import { DetailField } from "@/components/DetailField";
import { DefGrid, Panel } from "@/components/page/Panel";
import type { ColumnDef } from "@/hooks/useColumnLayout";
import { useDataTable } from "@/hooks/useDataTable";
import { expandColumn, useExpandedRows } from "@/hooks/useExpandedRows";
import { usePermissions } from "@/hooks/usePermissions";
import { apiClient } from "@/lib/api-client";
import { apiPath } from "@/lib/api-path";
import { formatExactDateTime } from "@/lib/format";
import { cn } from "@/lib/utils";
import type { VMResponse } from "@/types/api";
import { useNutServers, useRunUpsCommand, useUpsCommandRuns } from "../api/ups-queries";
import { DANGER_OUTLINE } from "../lib/ups-style";
import type { UpsCommand, UpsCommandRun, UpsCommandTier, UpsDevice } from "../types/ups";
import { DateTime } from "@/components/DateTime";
import { useTranslation } from "react-i18next";
import i18n from "@/lib/i18n";

const TIERS: { tier: UpsCommandTier; title: string; note: string }[] = [
  { tier: "everyday", get title() { return i18n.t("power:upsCommands.everyday"); }, get note() { return i18n.t("power:upsCommands.theBeeperThePanelTest"); } },
  { tier: "test", get title() { return i18n.t("power:upsCommands.tests"); }, get note() { return i18n.t("power:upsCommands.aBatteryTestDrainsThe"); } },
  { tier: "powers_on", get title() { return i18n.t("power:upsCommands.powersOn"); }, get note() { return i18n.t("power:upsCommands.switchesPowerOnToWhat"); } },
  { tier: "driver", get title() { return i18n.t("power:upsCommands.driver"); }, get note() { return i18n.t("power:upsCommands.reloadsTheNUTDriverReadings"); } },
  {
    tier: "cuts_power",
    get title() { return i18n.t("power:upsCommands.cutsPower"); },
    get note() { return i18n.t("power:upsCommands.switchesOffThePowerThe"); },
  },
];

/**
 * The UPS's instant commands, grouped by what they can do to what it powers,
 * and the runs so far. Running one takes execute:ups; one that cuts the power
 * also takes the UPS's name typed and manage:node on every cluster it powers,
 * which the server checks.
 */
export function UpsCommands({ device }: { device: UpsDevice }) {
  const { t } = useTranslation("power");
  const { hasGlobalPermission, canManage } = usePermissions();
  const canRun = hasGlobalPermission("execute", "ups");
  // The server also wants manage:node on each cluster the UPS powers; one
  // who holds it on none could never cut the power.
  const canCutPower = canRun && canManage("node");
  const server = useNutServers().data?.find((s) => s.id === device.server_id);
  const hasAccount = server?.has_password === true;
  const run = useRunUpsCommand(device.id);
  const [confirming, setConfirming] = useState<UpsCommand | null>(null);
  const [typing, setTyping] = useState<UpsCommand | null>(null);

  const send = (cmd: UpsCommand, confirm?: string) => {
    run.mutate({ command: cmd.name, ...(confirm === undefined ? {} : { confirm }) });
  };
  const start = (cmd: UpsCommand) => {
    if (cmd.confirm === "typed") setTyping(cmd);
    else if (cmd.confirm === "plain") setConfirming(cmd);
    else send(cmd);
  };

  if (device.commands_listed_at === undefined) {
    return (
      <Panel>
        <p className="text-sm text-muted-foreground">
          {t("upsCommands.nexaraHasNotReadThis")}
        </p>
      </Panel>
    );
  }
  const commands = device.commands ?? [];
  return (
    <div className="space-y-4">
      <Panel title={t("upsCommands.commands")} aside={commands.length > 0 ? t("upsCommands.lengthFromUpsd", { length: String(commands.length) }) : undefined}>
        <div className="-mt-2 max-w-[70ch]">
          <AccountNote hasAccount={hasAccount} verifiedAt={server?.commands_verified_at ?? null} />
        </div>
        {commands.length === 0 && <p className="mt-3 text-sm text-muted-foreground">{t("upsCommands.upsdListsNoInstantCommands")}</p>}
        {/* A tier per band: what it can do on the left, its commands on the right. */}
        <div className="@container mt-3 divide-y border-t empty:hidden">
          {TIERS.map(({ tier, title, note }) => {
            const inTier = commands.filter((c) => c.tier === tier);
            if (inTier.length === 0) return null;
            const cuts = tier === "cuts_power";
            return (
              <section key={tier} className="grid gap-x-8 gap-y-3 py-4 last:pb-0 @2xl:grid-cols-[14rem_minmax(0,1fr)]">
                <div>
                  <h3 className={cn("flex items-center gap-1.5 text-sm font-semibold", cuts && "text-red-700 dark:text-red-400")}>
                    {cuts && <AlertTriangle className="h-4 w-4" aria-hidden="true" />}
                    {title}
                  </h3>
                  <p className="mt-0.5 text-[12.5px] text-muted-foreground">{note}</p>
                </div>
                <ul className="space-y-3">
                  {inTier.map((cmd) => (
                    <li key={cmd.name} className="flex items-center justify-between gap-3">
                      <div className="min-w-0 flex-1">
                        <p className="break-all font-mono text-[13px]">{cmd.name}</p>
                        {cmd.description !== "" && <p className="text-[12.5px] text-muted-foreground">{cmd.description}</p>}
                      </div>
                      {/* Never a filled button: the one that cuts power is marked, not made the loudest. */}
                      {(cuts ? canCutPower : canRun) && (
                        <Button
                          size="sm"
                          className={cn("shrink-0", cuts && DANGER_OUTLINE)}
                          variant="outline"
                          disabled={!hasAccount || run.isPending}
                          aria-label={t("upsCommands.runName", { name: cmd.name })}
                          onClick={() => {
                            start(cmd);
                          }}
                        >
                          {t("upsCommands.run")}
                        </Button>
                      )}
                    </li>
                  ))}
                </ul>
              </section>
            );
          })}
        </div>
      </Panel>
      <RunHistory upsId={device.id} />

      <AlertDialog
        open={confirming !== null}
        onOpenChange={(open) => {
          if (!open) setConfirming(null);
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t("upsCommands.runNameOnName2", { name: confirming?.name, name2: device.name })}</AlertDialogTitle>
            <AlertDialogDescription>
              {TIERS.find((t) => t.tier === confirming?.tier)?.note} {confirming?.description}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t("upsCommands.cancel")}</AlertDialogCancel>
            <AlertDialogAction
              onClick={() => {
                if (confirming !== null) send(confirming);
              }}
            >
              {t("upsCommands.run")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <TypeToConfirmDialog
        target={typing}
        onClose={() => {
          setTyping(null);
        }}
        onConfirm={(cmd, typed) => {
          send(cmd, typed);
        }}
        title={(cmd) => t("upsCommands.runNameOnName2", { name: cmd.name, name2: device.name })}
        description={(cmd) =>
          cmd.description === ""
            ? t("upsCommands.thisSwitchesOffThePower", { name: device.name })
            : t("upsCommands.thisSwitchesOffThePowerDescribed", { name: device.name, description: cmd.description })
        }
        name={() => device.name}
        confirmLabel={t("upsCommands.cutThePower")}
        details={() => <WhatItPowers device={device} />}
      />
    </div>
  );
}

function AccountNote({ hasAccount, verifiedAt }: { hasAccount: boolean; verifiedAt: string | null }) {
  const { t } = useTranslation("power");
  if (!hasAccount) {
    return (
      <p className="text-sm text-muted-foreground">
        {t("upsCommands.upsdRunsCommandsOnlyFor")}
      </p>
    );
  }
  return (
    <p className="text-sm text-muted-foreground">
      {verifiedAt === null
        ? t("upsCommands.upsdChecksTheStoredAccount")
        : t("upsCommands.upsdLastTookACommand", { verifiedAt: formatExactDateTime(verifiedAt) })}
    </p>
  );
}

/**
 * The nodes the UPS powers, as far as the caller may see, with the guests
 * running on each, and a warning when the NUT server runs on one of them.
 */
function WhatItPowers({ device }: { device: UpsDevice }) {
  const { t } = useTranslation("power");
  const nodes = device.feeds.filter((f) => f.kind === "node");
  const clusters = [...new Set(nodes.flatMap((f) => (f.cluster_id === null ? [] : [f.cluster_id])))];
  const guests = useQueries({
    queries: clusters.map((id) => ({
      // useClusterVMs' key, so the two share their cache.
      queryKey: ["clusters", id, "vms"],
      queryFn: () => apiClient.list<VMResponse>(apiPath`/api/v1/clusters/${id}/vms`),
    })),
  });
  const running = (nodeId: string | null) => {
    if (nodeId === null) return null;
    const lists = guests.map((q) => q.data);
    if (lists.some((l) => l === undefined)) return null;
    return lists.flat().filter((vm) => vm?.node_id === nodeId && vm.status === "running" && !vm.template).length;
  };
  const host = device.host_node;
  const hostFed = host !== null && nodes.some((f) => f.cluster_id === host.cluster_id && f.node_name === host.node_name);
  const labels = device.feeds.filter((f) => f.kind === "label");
  return (
    <div className="space-y-2 text-sm">
      {nodes.length === 0 && labels.length === 0 ? (
        <p className="text-muted-foreground">{t("upsCommands.nexaraKnowsNothingThisUPS")}</p>
      ) : (
        <ul className="space-y-1">
          {nodes.map((f) => {
            const n = running(f.node_id);
            return (
              <li key={f.id}>
                <span className="font-medium">{f.node_name}</span>{" "}
                <span className="text-muted-foreground">
                  ({f.cluster_name}){n === null ? "" : n === 1 ? t("upsCommands.n1GuestRunning") : t("upsCommands.nGuestsRunning", { n: String(n) })}
                </span>
              </li>
            );
          })}
          {labels.map((f) => (
            <li key={f.id}>{f.label}</li>
          ))}
        </ul>
      )}
      {hostFed && (
        <p className="flex items-start gap-1.5 text-red-700 dark:text-red-400">
          <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
          {t("upsCommands.serverNameRunsOnNodeNameCutting", { serverName: device.server_name, nodeName: host.node_name })}
        </p>
      )}
    </div>
  );
}

const STATUS_VARIANT: Record<UpsCommandRun["status"], "default" | "secondary" | "destructive" | "outline"> = {
  sent: "outline",
  pending: "outline",
  succeeded: "secondary",
  failed: "destructive",
  unconfirmed: "outline",
};

type RunKey = "expand" | "when" | "who" | "what" | "status";

const RUN_COLUMNS: ColumnDef<UpsCommandRun, RunKey, { expanded: Set<string> }>[] = [
  expandColumn("expand"),
  { key: "when", get label() { return i18n.t("power:upsCommands.when"); }, width: 180, sortValue: (r) => r.requested_at, cell: (r) => <DateTime value={r.requested_at} compact /> },
  { key: "who", get label() { return i18n.t("power:upsCommands.who"); }, width: 160, sortValue: (r) => r.user_display_name || r.user_email, cell: (r) => r.user_display_name || r.user_email || "—" },
  {
    key: "what",
    get label() { return i18n.t("power:upsCommands.what"); },
    width: 380,
    wrap: true,
    sortValue: (r) => r.name,
    cell: (r) => (
      <span className="break-all font-mono text-xs">{r.kind === "set" ? `${r.name} = ${r.argument}` : r.name}</span>
    ),
  },
  {
    key: "status",
    get label() { return i18n.t("power:upsCommands.outcome"); },
    width: 160,
    sortValue: (r) => r.status,
    cell: (r) => (
      <Badge variant={STATUS_VARIANT[r.status]}>{r.status === "pending" || r.status === "sent" ? i18n.t("power:upsCommands.waiting") : r.status}</Badge>
    ),
  },
];

/** The UPS's newest runs and variable sets, live while one waits on its driver. */
function RunHistory({ upsId }: { upsId: string }) {
  const { t } = useTranslation("power");
  const runs = useUpsCommandRuns(upsId);
  const { layout, rows, toggle, directionFor } = useDataTable("ups-command-runs", RUN_COLUMNS, runs.data ?? [], (r) => r.id);
  const { expanded, toggle: toggleExpand } = useExpandedRows();
  if ((runs.data ?? []).length === 0) return null;
  return (
    <Panel flush title={t("upsCommands.runs")} aside={t("upsCommands.commandsRunAndVariablesSet")}>
      <DataTableFrame layout={layout}>
        <TableHeader>
          <DataTableHeadRow layout={layout} directionFor={directionFor} onSort={toggle} />
        </TableHeader>
        <TableBody>
          {rows.map((r) => (
            <Fragment key={r.id}>
              <TableRow className="cursor-pointer" onClick={() => { toggleExpand(r.id); }}>
                <DataTableCells row={r} layout={layout} ctx={{ expanded }} />
              </TableRow>
              {expanded.has(r.id) && (
                <ExpandedDetailRow colSpan={layout.columns.length}>
                  <DefGrid>
                    <DetailField label={t("upsCommands.outcome")}>{r.result || (r.status === "succeeded" ? t("upsCommands.theDriverRanIt") : "—")}</DetailField>
                    <DetailField label={t("upsCommands.finished")}><DateTime value={r.finished_at} /></DetailField>
                    {r.kind === "set" && <DetailField label={t("upsCommands.before")}>{r.previous ?? "—"}</DetailField>}
                  </DefGrid>
                </ExpandedDetailRow>
              )}
            </Fragment>
          ))}
        </TableBody>
      </DataTableFrame>
    </Panel>
  );
}
