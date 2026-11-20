import { useState, useMemo } from "react";
import { Monitor, TerminalSquare, Terminal, Plus, Search } from "lucide-react";
import { useQueries } from "@tanstack/react-query";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { StatusIcon } from "@/components/StatusIcon";
import { apiClient } from "@/lib/api-client";
import { apiPath } from "@/lib/api-path";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import { usePermissions } from "@/hooks/usePermissions";
import type { VMResponse, NodeResponse } from "@/types/api";
import type { ConsoleClickKeys } from "../lib/console-session";
import { guestState } from "@/lib/guest-state";
import { openConsole } from "../lib/open-console";
import { useTranslation } from "react-i18next";

interface NodeEntry {
  clusterId: string;
  clusterName: string;
  nodeName: string;
  status: string;
}

interface VMEntry {
  clusterId: string;
  clusterName: string;
  nodeName: string;
  vmid: number;
  name: string;
  type: string;
  status: string;
  /** guestState(): what the dot shows, a halted VM's state included. */
  state: string;
  resourceId: string;
}

/** Whether any of `fields` contains the (trimmed, case-folded) query. */
function matches(query: string, fields: string[]): boolean {
  return query === "" || fields.some((f) => f.toLowerCase().includes(query));
}

export function QuickConnect({
  appearance = "primary",
}: {
  /**
   * `primary` is the Console page's one button; `compact` is the plus in the
   * floating window's bar, where a labelled button would crowd the tabs.
   */
  appearance?: "primary" | "compact";
}) {
  const { t } = useTranslation("console");
  const [open, setOpen] = useState(false);
  const [search, setSearch] = useState("");
  const { canConsole } = usePermissions();
  const canNode = canConsole("node");
  const canGuest = canConsole("vm") || canConsole("container");

  const { data: clusters } = useClusters();
  const multiCluster = (clusters?.length ?? 0) > 1;

  const nodeQueries = useQueries({
    queries: (clusters ?? []).map((cluster) => ({
      queryKey: ["clusters", cluster.id, "nodes"],
      queryFn: () =>
        apiClient.list<NodeResponse>(
          apiPath`/api/v1/clusters/${cluster.id}/nodes`,
        ),
      enabled: open,
    })),
  });

  const vmQueries = useQueries({
    queries: (clusters ?? []).map((cluster) => ({
      queryKey: ["clusters", cluster.id, "vms"],
      queryFn: () =>
        apiClient.list<VMResponse>(apiPath`/api/v1/clusters/${cluster.id}/vms`),
      enabled: open && canGuest,
    })),
  });

  const { nodeList, vmList } = useMemo(() => {
    const nodeEntries: NodeEntry[] = [];
    const entries: VMEntry[] = [];
    if (!clusters) return { nodeList: nodeEntries, vmList: entries };

    for (let i = 0; i < clusters.length; i++) {
      const cluster = clusters[i];
      if (!cluster) continue;
      const nodes = nodeQueries[i]?.data ?? [];
      const vms = vmQueries[i]?.data ?? [];

      const nodeMap = new Map<string, string>();
      for (const node of nodes) {
        nodeMap.set(node.id, node.name);
        if (canNode) {
          nodeEntries.push({
            clusterId: cluster.id,
            clusterName: cluster.name,
            nodeName: node.name,
            status: node.status.toLowerCase(),
          });
        }
      }

      for (const vm of vms) {
        if (vm.template) continue;
        // Only list guests the user can actually console — the mint 403s
        // without the console:* permission for the guest's kind.
        if (!canConsole(vm.type === "qemu" ? "vm" : "container")) continue;
        entries.push({
          clusterId: cluster.id,
          clusterName: cluster.name,
          nodeName: nodeMap.get(vm.node_id) ?? "",
          vmid: vm.vmid,
          name: vm.name,
          type: vm.type,
          status: vm.status.toLowerCase(),
          state: guestState(vm.status, vm.run_state),
          resourceId: vm.id,
        });
      }
    }

    nodeEntries.sort(
      (a, b) =>
        a.clusterName.localeCompare(b.clusterName) ||
        a.nodeName.localeCompare(b.nodeName, undefined, { numeric: true }),
    );
    entries.sort((a, b) => a.vmid - b.vmid);
    return { nodeList: nodeEntries, vmList: entries };
  }, [clusters, nodeQueries, vmQueries, canConsole, canNode]);

  const query = search.trim().toLowerCase();
  const shownNodes = nodeList.filter((n) =>
    matches(query, [n.nodeName, n.clusterName]),
  );
  const shownGuests = vmList.filter((vm) =>
    matches(query, [vm.name, String(vm.vmid), vm.nodeName, vm.clusterName]),
  );
  const nothingToList = nodeList.length === 0 && vmList.length === 0;

  function setDialogOpen(next: boolean) {
    setOpen(next);
    if (!next) setSearch("");
  }

  function openNode(node: NodeEntry, keys: ConsoleClickKeys) {
    openConsole(
      { clusterID: node.clusterId, node: node.nodeName, type: "node_shell" },
      keys,
    );
    setDialogOpen(false);
  }

  function openGuest(
    vm: VMEntry,
    consoleType: "vnc" | "serial" | "attach",
    keys: ConsoleClickKeys,
  ) {
    const type =
      consoleType === "vnc"
        ? ("vm_vnc" as const)
        : consoleType === "attach"
          ? ("ct_vnc" as const)
          : ("vm_serial" as const);

    openConsole(
      {
        clusterID: vm.clusterId,
        node: vm.nodeName,
        vmid: vm.vmid,
        type,
        resourceId: vm.resourceId,
        kind: vm.type === "qemu" ? "vm" : "ct",
        guestName: vm.name,
      },
      keys,
    );
    setDialogOpen(false);
  }

  // Without console on a node or on either guest kind its list would be empty.
  if (!canNode && !canGuest) return null;

  const sectionHeading = (label: string) => (
    <li className="sticky top-0 z-10 border-t bg-popover py-1 pl-6 pr-4 first:border-t-0 text-[11.5px] font-medium uppercase tracking-wide text-muted-foreground">
      {label}
    </li>
  );

  return (
    <>
      {appearance === "compact" ? (
        <Button
          variant="ghost"
          size="icon"
          className="h-7 w-7 text-muted-foreground"
          aria-label={t("quickConnect.newConsole")}
          title={t("quickConnect.newConsole")}
          onClick={() => {
            setDialogOpen(true);
          }}
        >
          <Plus className="h-4 w-4" />
        </Button>
      ) : (
        <Button
          size="sm"
          className="gap-1.5"
          onClick={() => {
            setDialogOpen(true);
          }}
        >
          <Plus className="h-4 w-4" />
          {t("quickConnect.newConsole")}
        </Button>
      )}

      <Dialog open={open} onOpenChange={setDialogOpen}>
        <DialogContent className="max-w-lg gap-3">
          <DialogHeader>
            <DialogTitle>{t("quickConnect.openAConsole")}</DialogTitle>
            <DialogDescription>
              {canNode && canGuest
                ? t("quickConnect.chooseANodeOrGuest")
                : canNode
                  ? t("quickConnect.chooseANode")
                  : t("quickConnect.chooseARunningGuestTo")}
            </DialogDescription>
          </DialogHeader>

          {nothingToList ? (
            <p className="py-6 text-center text-sm text-muted-foreground">
              {canNode && canGuest
                ? t("quickConnect.noNodesOrGuestsFound")
                : canNode
                  ? t("quickConnect.noNodesFound")
                  : t("quickConnect.noVMsOrContainersFound")}
            </p>
          ) : (
            <>
              <div className="relative">
                <Search className="absolute left-2.5 top-2.5 h-4 w-4 text-muted-foreground" />
                <Input
                  aria-label={t("quickConnect.search")}
                  placeholder={t("quickConnect.searchPlaceholder")}
                  value={search}
                  onChange={(e) => {
                    setSearch(e.target.value);
                  }}
                  className="pl-9"
                />
              </div>

              {/* A fixed height, so the dialog does not jump as the list narrows. */}
              <ul className="-mx-6 h-80 overflow-y-auto border-y">
                {shownNodes.length === 0 && shownGuests.length === 0 && (
                  <li className="py-6 text-center text-sm text-muted-foreground">
                    {t("quickConnect.nothingMatches")}
                  </li>
                )}

                {shownNodes.length > 0 && (
                  <>
                    {sectionHeading(t("quickConnect.nodes"))}
                    {shownNodes.map((node) => (
                      <li
                        key={`${node.clusterId}-${node.nodeName}`}
                        className="flex items-center gap-3 border-t py-1.5 pl-6 pr-4 first:border-t-0"
                      >
                        <StatusIcon status={node.status} />
                        <div className="min-w-0 flex-1">
                          <span className="block truncate text-sm font-medium">
                            {node.nodeName}
                          </span>
                          {multiCluster && (
                            <span className="block truncate text-[12.5px] text-muted-foreground">
                              {node.clusterName}
                            </span>
                          )}
                        </div>
                        <Button
                          variant="ghost"
                          size="sm"
                          className="h-7 shrink-0 gap-1.5 px-2 text-[12.5px]"
                          disabled={node.status !== "online"}
                          title={t("quickConnect.nodeShell")}
                          onClick={(e) => {
                            openNode(node, e);
                          }}
                        >
                          <Terminal className="h-3.5 w-3.5" />
                          {t("quickConnect.shell")}
                        </Button>
                      </li>
                    ))}
                  </>
                )}

                {shownGuests.length > 0 && (
                  <>
                    {canNode && sectionHeading(t("quickConnect.guests"))}
                    {shownGuests.map((vm) => {
                      const isRunning = vm.status === "running";
                      const isVM = vm.type === "qemu";

                      return (
                        <li
                          key={`${vm.clusterId}-${String(vm.vmid)}`}
                          className="flex items-center gap-3 border-t py-1.5 pl-6 pr-4 first:border-t-0"
                        >
                          <StatusIcon status={vm.state} />
                          <div className="min-w-0 flex-1">
                            <div className="flex items-baseline gap-2">
                              <span className="truncate text-sm font-medium">
                                {vm.name}
                              </span>
                              <span className="shrink-0 text-[12.5px] tabular-nums text-muted-foreground">
                                {String(vm.vmid)}
                              </span>
                            </div>
                            {multiCluster && (
                              <span className="block truncate text-[12.5px] text-muted-foreground">
                                {vm.clusterName} / {vm.nodeName}
                              </span>
                            )}
                          </div>

                          {isVM ? (
                            <Button
                              variant="ghost"
                              size="sm"
                              className="h-7 shrink-0 gap-1.5 px-2 text-[12.5px]"
                              disabled={!isRunning}
                              title={t("quickConnect.vncConsole")}
                              onClick={(e) => {
                                openGuest(vm, "vnc", e);
                              }}
                            >
                              <Monitor className="h-3.5 w-3.5" />
                              {t("quickConnect.vnc")}
                            </Button>
                          ) : (
                            <Button
                              variant="ghost"
                              size="sm"
                              className="h-7 shrink-0 gap-1.5 px-2 text-[12.5px]"
                              disabled={!isRunning}
                              title={t("quickConnect.containerAttach")}
                              onClick={(e) => {
                                openGuest(vm, "attach", e);
                              }}
                            >
                              <TerminalSquare className="h-3.5 w-3.5" />
                              {t("quickConnect.attach")}
                            </Button>
                          )}
                        </li>
                      );
                    })}
                  </>
                )}
              </ul>
            </>
          )}
        </DialogContent>
      </Dialog>
    </>
  );
}
