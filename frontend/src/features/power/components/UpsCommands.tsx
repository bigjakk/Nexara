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
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { TableBody, TableHeader, TableRow } from "@/components/ui/table";
import { TypeToConfirmDialog } from "@/components/TypeToConfirmDialog";
import { DataTableFrame } from "@/components/DataTableFrame";
import { DataTableHeadRow } from "@/components/DataTableHeadCells";
import { DataTableCells } from "@/components/DataTableCells";
import { ExpandedDetailRow } from "@/components/ExpandedDetailRow";
import { DetailField } from "@/components/DetailField";
import type { ColumnDef } from "@/hooks/useColumnLayout";
import { useDataTable } from "@/hooks/useDataTable";
import { expandColumn, useExpandedRows } from "@/hooks/useExpandedRows";
import { usePermissions } from "@/hooks/usePermissions";
import { apiClient } from "@/lib/api-client";
import { apiPath } from "@/lib/api-path";
import { formatDateTime } from "@/lib/format";
import type { VMResponse } from "@/types/api";
import { useNutServers, useRunUpsCommand, useUpsCommandRuns } from "../api/ups-queries";
import type { UpsCommand, UpsCommandRun, UpsCommandTier, UpsDevice } from "../types/ups";

const TIERS: { tier: UpsCommandTier; title: string; note: string }[] = [
  { tier: "everyday", title: "Everyday", note: "The beeper, the panel test, and stopping something, such as a shutdown in progress." },
  { tier: "test", title: "Tests", note: "A battery test drains the battery; a deep one runs it down to low." },
  { tier: "powers_on", title: "Powers on", note: "Switches power on to what the UPS feeds." },
  { tier: "driver", title: "Driver", note: "Reloads the NUT driver; readings pause while it does." },
  {
    tier: "cuts_power",
    title: "Cuts power",
    note: "Switches off the power the UPS supplies, now or after a delay. A command Nexara does not recognise is here too.",
  },
];

/**
 * The UPS's instant commands, grouped by what they can do to what it powers,
 * and the runs so far. Running one takes execute:ups; one that cuts the power
 * also takes the UPS's name typed and manage:node on every cluster it powers,
 * which the server checks.
 */
export function UpsCommands({ device }: { device: UpsDevice }) {
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
      <p className="text-sm text-muted-foreground">
        Nexara has not read this UPS&apos;s commands yet. It lists them within a minute of reading the UPS.
      </p>
    );
  }
  const commands = device.commands ?? [];
  return (
    <div className="space-y-4">
      <AccountNote hasAccount={hasAccount} verifiedAt={server?.commands_verified_at ?? null} />
      {commands.length === 0 && <p className="text-sm text-muted-foreground">upsd lists no instant commands for this UPS.</p>}
      {TIERS.map(({ tier, title, note }) => {
        const inTier = commands.filter((c) => c.tier === tier);
        if (inTier.length === 0) return null;
        return (
          <Card key={tier}>
            <CardHeader className="pb-2">
              <CardTitle className="flex items-center gap-2 text-sm">
                {tier === "cuts_power" && <AlertTriangle className="h-4 w-4 text-destructive" aria-hidden="true" />}
                {title}
              </CardTitle>
              <p className="text-xs text-muted-foreground">{note}</p>
            </CardHeader>
            <CardContent className="divide-y">
              {inTier.map((cmd) => (
                <div key={cmd.name} className="flex items-start justify-between gap-3 py-2">
                  <div className="min-w-0 flex-1">
                    <p className="break-all font-mono text-sm">{cmd.name}</p>
                    {cmd.description !== "" && <p className="text-xs text-muted-foreground">{cmd.description}</p>}
                  </div>
                  {(tier === "cuts_power" ? canCutPower : canRun) && (
                    <Button
                      size="sm"
                      className="shrink-0"
                      variant={tier === "cuts_power" ? "destructive" : "outline"}
                      disabled={!hasAccount || run.isPending}
                      aria-label={`Run ${cmd.name}`}
                      onClick={() => {
                        start(cmd);
                      }}
                    >
                      Run
                    </Button>
                  )}
                </div>
              ))}
            </CardContent>
          </Card>
        );
      })}
      <RunHistory upsId={device.id} />

      <AlertDialog
        open={confirming !== null}
        onOpenChange={(open) => {
          if (!open) setConfirming(null);
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Run {confirming?.name} on {device.name}?</AlertDialogTitle>
            <AlertDialogDescription>
              {TIERS.find((t) => t.tier === confirming?.tier)?.note} {confirming?.description}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              onClick={() => {
                if (confirming !== null) send(confirming);
              }}
            >
              Run
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
        title={(cmd) => `Run ${cmd.name} on ${device.name}?`}
        description={(cmd) =>
          `This switches off the power ${device.name} supplies${cmd.description === "" ? "" : ` (${cmd.description})`}. Everything it feeds goes down without shutting down first.`
        }
        name={() => device.name}
        confirmLabel="Cut the power"
        details={() => <WhatItPowers device={device} />}
      />
    </div>
  );
}

function AccountNote({ hasAccount, verifiedAt }: { hasAccount: boolean; verifiedAt: string | null }) {
  if (!hasAccount) {
    return (
      <p className="text-sm text-muted-foreground">
        upsd runs commands only for an account from its upsd.users. Add one with instcmds to this UPS&apos;s NUT server, on
        the Power page&apos;s NUT servers tab.
      </p>
    );
  }
  return (
    <p className="text-sm text-muted-foreground">
      {verifiedAt === null
        ? "upsd checks the stored account only when a command runs, so the first command is its test."
        : `upsd last took a command from the stored account on ${formatDateTime(verifiedAt)}.`}
    </p>
  );
}

/**
 * The nodes the UPS powers, as far as the caller may see, with the guests
 * running on each, and a warning when the NUT server runs on one of them.
 */
function WhatItPowers({ device }: { device: UpsDevice }) {
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
        <p className="text-muted-foreground">Nexara knows nothing this UPS powers.</p>
      ) : (
        <ul className="space-y-1">
          {nodes.map((f) => {
            const n = running(f.node_id);
            return (
              <li key={f.id}>
                <span className="font-medium">{f.node_name}</span>{" "}
                <span className="text-muted-foreground">
                  ({f.cluster_name}){n === null ? "" : n === 1 ? ", 1 guest running" : `, ${String(n)} guests running`}
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
        <p className="flex items-start gap-1.5 text-destructive">
          <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
          {device.server_name} runs on {host.node_name}: cutting the power stops it too, and Nexara may not hear how the command
          went.
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
  { key: "when", label: "When", width: 180, sortValue: (r) => r.requested_at, cell: (r) => formatDateTime(r.requested_at) },
  { key: "who", label: "Who", width: 160, sortValue: (r) => r.user_display_name || r.user_email, cell: (r) => r.user_display_name || r.user_email || "—" },
  {
    key: "what",
    label: "What",
    width: 280,
    wrap: true,
    sortValue: (r) => r.name,
    cell: (r) => (
      <span className="break-all font-mono text-xs">{r.kind === "set" ? `${r.name} = ${r.argument}` : r.name}</span>
    ),
  },
  {
    key: "status",
    label: "Outcome",
    width: 130,
    sortValue: (r) => r.status,
    cell: (r) => (
      <Badge variant={STATUS_VARIANT[r.status]}>{r.status === "pending" || r.status === "sent" ? "waiting" : r.status}</Badge>
    ),
  },
];

/** The UPS's newest runs and variable sets, live while one waits on its driver. */
function RunHistory({ upsId }: { upsId: string }) {
  const runs = useUpsCommandRuns(upsId);
  const { layout, rows, toggle, directionFor } = useDataTable("ups-command-runs", RUN_COLUMNS, runs.data ?? [], (r) => r.id);
  const { expanded, toggle: toggleExpand } = useExpandedRows();
  if ((runs.data ?? []).length === 0) return null;
  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="text-sm">Runs</CardTitle>
      </CardHeader>
      <CardContent>
        <div className="rounded-md border">
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
                      <div className="grid gap-2 sm:grid-cols-2">
                        <DetailField label="Outcome">{r.result || (r.status === "succeeded" ? "The driver ran it" : "—")}</DetailField>
                        <DetailField label="Finished">{formatDateTime(r.finished_at)}</DetailField>
                        {r.kind === "set" && <DetailField label="Before">{r.previous ?? "—"}</DetailField>}
                      </div>
                    </ExpandedDetailRow>
                  )}
                </Fragment>
              ))}
            </TableBody>
          </DataTableFrame>
        </div>
      </CardContent>
    </Card>
  );
}
