import { useMemo, useState } from "react";
import { useParams, Link, useSearchParams } from "react-router-dom";
import { Server, ServerOff, Terminal } from "lucide-react";
import { ResourceTable } from "@/features/inventory/components/ResourceTable";
import { InventoryUnavailableNote } from "@/features/inventory/components/InventoryUnavailableNote";
import { useInventoryData } from "@/features/inventory/api/inventory-queries";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import { EmptyState } from "@/components/EmptyState";
import { DetailHeader, Fact, StatusFact } from "@/components/page/DetailHeader";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { NodeUpdatePanel } from "@/features/rolling-updates/components/NodeUpdatePanel";
import { useClusterMetrics } from "@/hooks/useMetrics";
import { useClusterNodes, useClusterVMs } from "../api/cluster-queries";
import { openConsole } from "@/features/console/lib/open-console";
import type { ConsoleClickKeys } from "@/features/console/lib/console-session";
import { usePermissions } from "@/hooks/usePermissions";
import { NodeAptRepositories } from "../components/NodeAptRepositories";
import { NodeHistoryTab } from "../components/NodeHistoryTab";
import { NodePowerActions } from "../components/node/NodePowerActions";
import { NodeMetricsPanel } from "../components/node/NodeMetricsPanel";
import { NodeGuestsPanel } from "../components/node/NodeGuestsPanel";
import { NodeHardwarePanel } from "../components/node/NodeHardwarePanel";
import { NodeSystemPanel } from "../components/node/NodeSystemPanel";
import { NodeNotesCard } from "../components/node/NodeNotesCard";
import { NodeOptionsCard } from "../components/node/NodeOptionsCard";
import {
  NodeTemperatures,
  NodeTemperaturesNote,
} from "../components/node/NodeTemperatures";
import { NodePowerCard } from "@/features/power/components/NodePowerCard";
import {
  NodeMemoryModules,
  NodeMemoryModulesNote,
} from "../components/node/NodeMemoryModules";
import { NetworkTab } from "../components/node/NodeNetworkTab";
import { DisksTab } from "../components/node/NodeDisksTab";
import { ServicesTab } from "../components/node/NodeServicesTab";
import { FirewallTab } from "../components/node/NodeFirewallTab";
import { SyslogTab } from "../components/node/NodeSyslogTab";
import { PCIDevicesTab } from "../components/node/NodePCIDevicesTab";
import { formatBytes, formatUptime } from "@/lib/format";

export function NodeDetailPage() {
  const { clusterId = "", nodeId = "" } = useParams<{
    clusterId: string;
    nodeId: string;
  }>();
  const [searchParams, setSearchParams] = useSearchParams();
  const tabParam = searchParams.get("tab") ?? "";
  // Controlled + URL-synced tabs — same rationale as ClusterDetailPage: a
  // bare `value` without onValueChange froze the tab bar for ?tab= arrivals.
  const setTab = (v: string) => {
    setSearchParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        if (v === "summary") next.delete("tab");
        else next.set("tab", v);
        return next;
      },
      { replace: true },
    );
  };
  const { data: nodes, isLoading } = useClusterNodes(clusterId);
  const node = nodes?.find((n) => n.id === nodeId);
  // The cluster's name, for the breadcrumb: the list the inventory already
  // reads, so the page asks for nothing more.
  const { data: clusters } = useClusters();
  const clusterName = clusters?.find((c) => c.id === clusterId)?.name;

  const clusterMetrics = useClusterMetrics(clusterId);

  // Guests resident on this node, as inventory rows so the VMs tab gets the
  // full ResourceTable (context menu, bulk actions) like the folder view.
  const { data: clusterVMs } = useClusterVMs(clusterId);
  const {
    rows: inventoryRows,
    failedClusterIds,
    isLoading: inventoryLoading,
  } = useInventoryData();
  const nodeVmRows = useMemo(() => {
    const onNode = new Set(
      (clusterVMs ?? [])
        .filter((vm) => vm.node_id === nodeId)
        .map((vm) => vm.id),
    );
    return inventoryRows.filter(
      (row) =>
        row.clusterId === clusterId &&
        row.type !== "node" &&
        onNode.has(row.id),
    );
  }, [clusterVMs, inventoryRows, clusterId, nodeId]);
  const liveMetric = clusterMetrics?.nodeMetrics.get(nodeId);
  const { canConsole, canManage, hasPermission } = usePermissions();
  // History is read from the audit log, and its rows open Proxmox task logs:
  // without both grants the tab would be a table of refusals.
  const canHistory =
    hasPermission("view", "audit") && hasPermission("view", "task");
  // A failed action, and the note that a support bundle is on its way, get
  // the full width under the header; the buttons sit in the header's corner.
  const [bannerSlot, setBannerSlot] = useState<HTMLDivElement | null>(null);

  function openShell(keys: ConsoleClickKeys) {
    if (!node) return;
    openConsole(
      { clusterID: clusterId, node: node.name, type: "node_shell" },
      keys,
    );
  }

  if (isLoading) {
    return (
      <div className="space-y-4">
        <Skeleton className="h-8 w-48" />
        <Skeleton className="h-48 w-full" />
      </div>
    );
  }

  if (!node) {
    return (
      <div>
        <p className="text-destructive">Node not found</p>
        <Button variant="link" asChild className="mt-2 px-0">
          <Link to={`/clusters/${clusterId}`}>Back to Cluster</Link>
        </Button>
      </div>
    );
  }

  const online = node.status === "online";
  const inventoryFailed = failedClusterIds.includes(clusterId);
  // Until the guest list has answered, or when it could not be read, an empty
  // list is not "no guests": the header then says nothing of them.
  const guestsKnown = !inventoryLoading && !inventoryFailed;
  // Who is shown the timezone and DNS dialogs, and who makes the reads they edit
  // from: see NodeSystemPanel.
  const canEditSettings = online && canManage("node");
  const pveVersion = node.pve_version.replace(/^pve-manager\/([^/]+).*$/, "$1");
  const statusLabel =
    node.status.charAt(0).toUpperCase() + node.status.slice(1);

  return (
    <div className="space-y-5">
      <DetailHeader
        crumbs={[
          { label: "Inventory", to: "/inventory" },
          { label: clusterName ?? "Cluster", to: `/clusters/${clusterId}` },
          { label: node.name },
        ]}
        icon={<Server />}
        title={node.name}
        facts={
          <>
            <StatusFact
              status={node.status}
              label={statusLabel}
              detail={
                online && node.uptime > 0
                  ? `up ${formatUptime(node.uptime)}`
                  : undefined
              }
            />
            {node.ha_state === "maintenance" && (
              <StatusFact status="maintenance" label="In maintenance" />
            )}
            {pveVersion !== "" && <Fact label="PVE">{pveVersion}</Fact>}
            <Fact label="vCPUs">{String(node.cpu_count)}</Fact>
            <Fact label="RAM">{formatBytes(node.mem_total)}</Fact>
            {guestsKnown && (
              <Fact label="Guests">{String(nodeVmRows.length)}</Fact>
            )}
          </>
        }
        actions={
          online ? (
            <NodePowerActions
              clusterId={clusterId}
              nodeName={node.name}
              inMaintenance={node.ha_state === "maintenance"}
              otherNodes={(nodes ?? [])
                .filter((n) => n.name !== node.name && n.status === "online")
                .map((n) => n.name)}
              canReport={canManage("node")}
              bannerContainer={bannerSlot}
            >
              {canConsole("node") && (
                <Button size="sm" className="gap-1.5" onClick={openShell}>
                  <Terminal className="h-4 w-4" />
                  Shell
                </Button>
              )}
            </NodePowerActions>
          ) : undefined
        }
      />
      <div ref={setBannerSlot} className="space-y-2 empty:hidden" />

      {/* Tabbed content */}
      <Tabs value={tabParam || "summary"} onValueChange={setTab}>
        <TabsList>
          <TabsTrigger value="summary">Summary</TabsTrigger>
          <TabsTrigger value="vms">VMs ({nodeVmRows.length})</TabsTrigger>
          <TabsTrigger value="network">Network</TabsTrigger>
          <TabsTrigger value="disks">Disks</TabsTrigger>
          <TabsTrigger value="services">Services</TabsTrigger>
          <TabsTrigger value="firewall">Firewall</TabsTrigger>
          <TabsTrigger value="syslog">Syslog</TabsTrigger>
          <TabsTrigger value="pci">PCI devices</TabsTrigger>
          <TabsTrigger value="updates">Updates</TabsTrigger>
          {canHistory && <TabsTrigger value="history">History</TabsTrigger>}
        </TabsList>

        {/* Summary Tab */}
        <TabsContent value="summary" className="mt-5">
          {/* Two columns that fold into one: what the node is doing on the
              left, what it is and how it is set up on the right. */}
          <div className="flex flex-wrap items-start gap-4">
            <div className="flex min-w-0 flex-[999_1_560px] flex-col gap-4">
              <NodeMetricsPanel
                clusterId={clusterId}
                nodeId={nodeId}
                liveMetric={liveMetric}
                memTotalBytes={node.mem_total}
                cpuCount={node.cpu_count}
                loadAvg={node.load_avg}
                ioWait={node.io_wait}
              />
              <NodeGuestsPanel
                rows={nodeVmRows}
                loading={inventoryLoading}
                unavailable={inventoryFailed}
                onViewAll={() => {
                  setTab("vms");
                }}
              />
              {/* Each only when it has something to show: without, the
                  Hardware panel says why in a line. */}
              <NodeTemperatures
                clusterId={clusterId}
                nodeName={node.name}
                online={online}
              />
              <NodeMemoryModules
                clusterId={clusterId}
                nodeName={node.name}
                online={online}
                canRead={canManage("node")}
              />
            </div>
            <div className="flex min-w-0 flex-[1_1_300px] flex-col gap-4">
              <NodeHardwarePanel node={node}>
                <NodeTemperaturesNote
                  clusterId={clusterId}
                  nodeName={node.name}
                  online={online}
                />
                <NodeMemoryModulesNote
                  clusterId={clusterId}
                  nodeName={node.name}
                  online={online}
                  canRead={canManage("node")}
                />
              </NodeHardwarePanel>
              <NodeSystemPanel
                clusterId={clusterId}
                node={node}
                canEdit={canEditSettings}
              />
              {/* Keyed on the node, not only rebuilt by AppShell's pathname
                  key: a dialog that survived a change of node would PUT the
                  old node's snapshot to the new one. Each has its own key,
                  because siblings cannot share one. */}
              <NodeOptionsCard
                key={`options-${node.id}`}
                clusterId={clusterId}
                nodeName={node.name}
                pveVersion={node.pve_version}
                online={online}
                canEdit={canManage("node")}
              />
              <NodeNotesCard
                key={`notes-${node.id}`}
                clusterId={clusterId}
                nodeName={node.name}
                online={online}
                canEdit={canManage("node")}
              />
              {/* The UPSes that power the node, for view:ups holders. */}
              <NodePowerCard
                clusterId={clusterId}
                nodeName={node.name}
                online={online}
              />
            </div>
          </div>
        </TabsContent>

        {/* VMs Tab */}
        <TabsContent value="vms" className="mt-4">
          {inventoryFailed && <InventoryUnavailableNote />}
          <ResourceTable data={nodeVmRows} />
        </TabsContent>

        {/* Network Tab */}
        <TabsContent value="network" className="mt-4">
          <NetworkTab clusterId={clusterId} nodeName={node.name} />
        </TabsContent>

        {/* Disks Tab */}
        <TabsContent value="disks" className="mt-4">
          <DisksTab
            clusterId={clusterId}
            nodeId={nodeId}
            nodeName={node.name}
            isOnline={online}
          />
        </TabsContent>

        {/* Services Tab */}
        <TabsContent value="services" className="mt-4">
          {online ? (
            <ServicesTab clusterId={clusterId} nodeName={node.name} />
          ) : (
            <OfflineNote>
              Services can be read only while the node is online.
            </OfflineNote>
          )}
        </TabsContent>

        {/* Firewall Tab */}
        <TabsContent value="firewall" className="mt-4">
          {online ? (
            <FirewallTab clusterId={clusterId} nodeName={node.name} />
          ) : (
            <OfflineNote>
              The firewall can be managed only while the node is online.
            </OfflineNote>
          )}
        </TabsContent>

        {/* Syslog Tab */}
        <TabsContent value="syslog" className="mt-4">
          {online ? (
            <SyslogTab clusterId={clusterId} nodeName={node.name} />
          ) : (
            <OfflineNote>
              The system log can be read only while the node is online.
            </OfflineNote>
          )}
        </TabsContent>

        {/* PCI Devices Tab */}
        <TabsContent value="pci" className="mt-4">
          <PCIDevicesTab clusterId={clusterId} nodeId={nodeId} />
        </TabsContent>

        {/* Updates Tab */}
        <TabsContent value="updates" className="mt-4 space-y-4">
          {online ? (
            <>
              {/* Above the repositories: what is pending and how to apply it is
                  the question this tab is opened with; where the packages come
                  from is the follow-up. */}
              <NodeUpdatePanel clusterId={clusterId} nodeName={node.name} />
              <NodeAptRepositories clusterId={clusterId} nodeName={node.name} />
            </>
          ) : (
            <OfflineNote>
              Updates can be read only while the node is online.
            </OfflineNote>
          )}
        </TabsContent>

        {/* History Tab */}
        {canHistory && (
          <TabsContent value="history" className="mt-4">
            <NodeHistoryTab clusterId={clusterId} nodeName={node.name} />
          </TabsContent>
        )}
      </Tabs>
    </div>
  );
}

/** What a tab that reads the node itself shows while the node is offline. */
function OfflineNote({ children }: { children: string }) {
  return (
    <EmptyState
      icon={ServerOff}
      title="This node is offline"
      description={children}
    />
  );
}
