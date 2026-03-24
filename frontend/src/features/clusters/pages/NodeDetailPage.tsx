import { useState } from "react";
import { useParams, Link, useSearchParams } from "react-router-dom";
import {
  ArrowLeft, Terminal, Cpu, MemoryStick, HardDrive, Info,
  Network, CircuitBoard, Globe, Trash2, RotateCcw, Check,
  Pencil, Power, RefreshCw, Cog, FileText, Play, Square, RotateCw,
  ArrowRightLeft, Plus,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/skeleton";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import { MetricMiniBar } from "@/features/inventory/components/MetricMiniBar";
import { MetricChart } from "@/features/dashboard/components/MetricChart";
import { useNodeHistoricalMetrics } from "@/features/dashboard/api/historical-queries";
import { useClusterMetrics } from "@/hooks/useMetrics";
import {
  useClusterNodes,
  useNodeDisks,
  useNodePCIDevices,
  useNodeDNS,
  useSetNodeDNS,
  useSetNodeTimezone,
  useShutdownNode,
  useRebootNode,
  useDiskSMART,
  useNodeZFSPools,
  useNodeLVM,
  useNodeLVMThin,
  useInitializeGPT,
  useWipeDisk,
  type ZFSPoolResponse,
  type LVMVolumeGroupResponse,
  type LVMThinPoolResponse,
  type DirectoryEntryResponse,
  type LiveDiskResponse,
  useLiveDisks,
  useCreateZFSPool,
  useDeleteZFSPool,
  useCreateLVM,
  useDeleteLVM,
  useCreateLVMThin,
  useDeleteLVMThin,
  useNodeDirectories,
  useCreateDirectory,
  useNodeServices,
  useServiceAction,
  useNodeSyslog,
  useNodeFirewallRules,
  useCreateNodeFirewallRule,
  useDeleteNodeFirewallRule,
  useNodeFirewallLog,
  useEvacuateNode,
  type EvacuateMigration,
} from "../api/cluster-queries";
import {
  useNodeNetworkInterfaces as useNodeNetworkInterfacesLive,
  useDeleteNetworkInterface,
  useUpdateNetworkInterface,
  useApplyNetworkConfig,
  useRevertNetworkConfig,
} from "@/features/networks/api/network-queries";
import type { UpdateNetworkInterfaceRequest } from "@/features/networks/types/network";
import { CreateInterfaceDialog } from "@/features/networks/components/CreateInterfaceDialog";
import { useConsoleStore } from "@/stores/console-store";
import { useTaskLogStore } from "@/stores/task-log-store";
import { NodeAptRepositories } from "../components/NodeAptRepositories";
import { formatBytes, formatUptime } from "@/lib/format";
import type { TimeRange } from "@/types/api";

const TIME_RANGES: { label: string; value: TimeRange }[] = [
  { label: "1h", value: "1h" },
  { label: "6h", value: "6h" },
  { label: "24h", value: "24h" },
  { label: "7d", value: "7d" },
];

export function NodeDetailPage() {
  const { clusterId = "", nodeId = "" } = useParams<{
    clusterId: string;
    nodeId: string;
  }>();
  const [searchParams] = useSearchParams();
  const tabParam = searchParams.get("tab") ?? "";
  const { data: nodes, isLoading } = useClusterNodes(clusterId);
  const node = nodes?.find((n) => n.id === nodeId);

  const clusterMetrics = useClusterMetrics(clusterId);
  const liveMetric = clusterMetrics?.nodeMetrics.get(nodeId);
  const addTab = useConsoleStore((s) => s.addTab);
  const showConsole = useConsoleStore((s) => s.showConsole);

  function openShell() {
    if (!node) return;
    addTab({
      clusterID: clusterId,
      node: node.name,
      type: "node_shell",
      label: `Shell: ${node.name}`,
    });
    showConsole();
  }

  if (isLoading) {
    return (
      <div className="space-y-4 p-6">
        <Skeleton className="h-8 w-48" />
        <Skeleton className="h-48 w-full" />
      </div>
    );
  }

  if (!node) {
    return (
      <div className="p-6">
        <p className="text-destructive">Node not found</p>
        <Button variant="link" asChild className="mt-2 px-0">
          <Link to={`/clusters/${clusterId}`}>Back to Cluster</Link>
        </Button>
      </div>
    );
  }

  const swapPercent = node.swap_total > 0 ? ((node.swap_used / node.swap_total) * 100).toFixed(1) : null;

  return (
    <div className="space-y-6 p-6">
      {/* Header */}
      <div className="flex items-start justify-between gap-4">
        <div className="space-y-1">
          <div className="flex items-center gap-3">
            <Button variant="ghost" size="sm" asChild className="-ml-2">
              <Link to={`/clusters/${clusterId}`}>
                <ArrowLeft className="h-4 w-4" />
              </Link>
            </Button>
            <h1 className="text-2xl font-bold">{node.name}</h1>
            <Badge variant={node.status === "online" ? "default" : "destructive"}>
              {node.status}
            </Badge>
          </div>
          <p className="text-sm text-muted-foreground">
            Proxmox Node
          </p>
        </div>
        {node.status === "online" && (
          <div className="flex items-center gap-2">
            <Button
              variant="outline"
              size="sm"
              className="gap-1.5"
              onClick={openShell}
            >
              <Terminal className="h-4 w-4" />
              Shell
            </Button>
            <NodePowerActions clusterId={clusterId} nodeName={node.name} otherNodes={(nodes ?? []).filter((n) => n.name !== node.name && n.status === "online").map((n) => n.name)} />
          </div>
        )}
      </div>

      {/* Tabbed content */}
      <Tabs defaultValue={tabParam || "summary"} {...(tabParam ? { value: tabParam } : {})}>
        <TabsList>
          <TabsTrigger value="summary">Summary</TabsTrigger>
          <TabsTrigger value="network">Network</TabsTrigger>
          <TabsTrigger value="disks">Disks</TabsTrigger>
          <TabsTrigger value="services">Services</TabsTrigger>
          <TabsTrigger value="firewall">Firewall</TabsTrigger>
          <TabsTrigger value="syslog">Syslog</TabsTrigger>
          <TabsTrigger value="pci">PCI Devices</TabsTrigger>
          <TabsTrigger value="updates">Updates</TabsTrigger>
        </TabsList>

        {/* Summary Tab */}
        <TabsContent value="summary" className="mt-4 space-y-6">
          {/* Hardware Info Cards */}
          <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
            <HardwareSection
              icon={<Cpu className="h-4 w-4" />}
              title="Processor"
              items={[
                { label: "Model", value: node.cpu_model || "--" },
                { label: "Sockets", value: node.cpu_sockets ? String(node.cpu_sockets) : "--" },
                { label: "Cores", value: node.cpu_cores ? `${String(node.cpu_cores)} per socket` : "--" },
                { label: "Threads", value: node.cpu_threads ? `${String(node.cpu_threads)} per core` : "--" },
                { label: "Total CPUs", value: String(node.cpu_count) },
                { label: "Frequency", value: node.cpu_mhz ? `${node.cpu_mhz} MHz` : "--" },
              ]}
            />
            <HardwareSection
              icon={<MemoryStick className="h-4 w-4" />}
              title="Memory"
              items={[
                { label: "Total RAM", value: formatBytes(node.mem_total) },
                { label: "Swap Total", value: node.swap_total ? formatBytes(node.swap_total) : "--" },
                { label: "Swap Used", value: node.swap_total ? `${formatBytes(node.swap_used)} (${swapPercent ?? "0"}%)` : "--" },
              ]}
            />
            <HardwareSectionWithAction
              icon={<Info className="h-4 w-4" />}
              title="System"
              items={[
                { label: "PVE Version", value: node.pve_version || "--" },
                { label: "Kernel", value: node.kernel_version || "--" },
                { label: "Uptime", value: formatUptime(node.uptime) },
                { label: "Load Average", value: node.load_avg || "--" },
                { label: "I/O Wait", value: node.io_wait > 0 ? `${(node.io_wait * 100).toFixed(1)}%` : "--" },
                { label: "Timezone", value: node.timezone || "--" },
              ]}
              action={
                node.status === "online" ? (
                  <EditTimezoneDialog clusterId={clusterId} nodeName={node.name} currentTimezone={node.timezone} />
                ) : undefined
              }
            />
            <HardwareSectionWithAction
              icon={<Globe className="h-4 w-4" />}
              title="Network & DNS"
              items={[
                { label: "DNS Servers", value: node.dns_servers || "--" },
                { label: "Search Domain", value: node.dns_search || "--" },
                ...(node.subscription_status ? [
                  { label: "Subscription", value: `${node.subscription_status}${node.subscription_level ? ` (${node.subscription_level})` : ""}` },
                ] : []),
              ]}
              action={
                node.status === "online" ? (
                  <EditDNSDialog clusterId={clusterId} nodeName={node.name} />
                ) : undefined
              }
            />
          </div>

          {/* Live metrics + charts */}
          <NodeMetricsPanel
            clusterId={clusterId}
            nodeId={nodeId}
            liveMetric={liveMetric}
          />
        </TabsContent>

        {/* Network Tab */}
        <TabsContent value="network" className="mt-4">
          <NetworkTab clusterId={clusterId} nodeName={node.name} />
        </TabsContent>

        {/* Disks Tab */}
        <TabsContent value="disks" className="mt-4">
          <DisksTab clusterId={clusterId} nodeId={nodeId} nodeName={node.name} isOnline={node.status === "online"} />
        </TabsContent>

        {/* Services Tab */}
        <TabsContent value="services" className="mt-4">
          {node.status === "online" ? (
            <ServicesTab clusterId={clusterId} nodeName={node.name} />
          ) : (
            <p className="text-sm text-muted-foreground">Node must be online to view services.</p>
          )}
        </TabsContent>

        {/* Firewall Tab */}
        <TabsContent value="firewall" className="mt-4">
          {node.status === "online" ? (
            <FirewallTab clusterId={clusterId} nodeName={node.name} />
          ) : (
            <p className="text-sm text-muted-foreground">Node must be online to manage firewall.</p>
          )}
        </TabsContent>

        {/* Syslog Tab */}
        <TabsContent value="syslog" className="mt-4">
          {node.status === "online" ? (
            <SyslogTab clusterId={clusterId} nodeName={node.name} />
          ) : (
            <p className="text-sm text-muted-foreground">Node must be online to view syslog.</p>
          )}
        </TabsContent>

        {/* PCI Devices Tab */}
        <TabsContent value="pci" className="mt-4">
          <PCIDevicesTab clusterId={clusterId} nodeId={nodeId} />
        </TabsContent>

        {/* Updates Tab */}
        <TabsContent value="updates" className="mt-4">
          {node.status === "online" ? (
            <NodeAptRepositories clusterId={clusterId} nodeName={node.name} />
          ) : (
            <p className="text-sm text-muted-foreground">
              Node must be online to view repositories.
            </p>
          )}
        </TabsContent>
      </Tabs>
    </div>
  );
}

/* ------------------------------------------------------------------ */
/* Tab content components                                             */
/* ------------------------------------------------------------------ */

function NetworkTab({ clusterId, nodeName }: { clusterId: string; nodeName: string }) {
  const { data: networkInterfaces, isLoading } = useNodeNetworkInterfacesLive(clusterId, nodeName);
  const deleteIface = useDeleteNetworkInterface(clusterId, nodeName);
  const updateIface = useUpdateNetworkInterface(clusterId, nodeName);
  const apply = useApplyNetworkConfig(clusterId, nodeName);
  const revert = useRevertNetworkConfig(clusterId, nodeName);
  const [editIface, setEditIface] = useState<string | null>(null);
  const [editCidr, setEditCidr] = useState("");
  const [editGateway, setEditGateway] = useState("");
  const [editBridgePorts, setEditBridgePorts] = useState("");

  if (isLoading) return <Skeleton className="h-48 w-full" />;

  const openEdit = (iface: { iface: string; type: string; cidr?: string; address?: string; gateway?: string; bridge_ports?: string }) => {
    setEditIface(iface.iface);
    setEditCidr(iface.cidr ?? iface.address ?? "");
    setEditGateway(iface.gateway ?? "");
    setEditBridgePorts(iface.bridge_ports ?? "");
  };

  const saveEdit = (ifaceName: string, ifaceType: string) => {
    const params: UpdateNetworkInterfaceRequest = { type: ifaceType };
    if (editCidr) params.cidr = editCidr;
    if (editGateway) params.gateway = editGateway;
    if (editBridgePorts) params.bridge_ports = editBridgePorts;
    updateIface.mutate(
      { iface: ifaceName, params },
      { onSuccess: () => { setEditIface(null); } },
    );
  };

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2">
          <Network className="h-4 w-4 text-muted-foreground" />
          <h2 className="text-lg font-semibold">Network Interfaces</h2>
        </div>
        <div className="flex items-center gap-2">
          <Button
            variant="outline"
            size="sm"
            onClick={() => { revert.mutate(); }}
            disabled={revert.isPending}
          >
            <RotateCcw className="mr-1 h-4 w-4" />
            Revert
          </Button>
          <Button
            variant="outline"
            size="sm"
            onClick={() => { apply.mutate(); }}
            disabled={apply.isPending}
          >
            <Check className="mr-1 h-4 w-4" />
            Apply Config
          </Button>
          <CreateInterfaceDialog clusterId={clusterId} nodeName={nodeName} />
        </div>
      </div>

      {!networkInterfaces || networkInterfaces.length === 0 ? (
        <p className="text-sm text-muted-foreground">No network interfaces found.</p>
      ) : (
        <div className="overflow-x-auto rounded-lg border">
          <table className="w-full text-sm">
            <thead className="border-b bg-muted/50">
              <tr>
                <th className="px-3 py-2 text-left font-medium">Interface</th>
                <th className="px-3 py-2 text-left font-medium">Type</th>
                <th className="px-3 py-2 text-left font-medium">Active</th>
                <th className="px-3 py-2 text-left font-medium">Address / CIDR</th>
                <th className="px-3 py-2 text-left font-medium">Gateway</th>
                <th className="px-3 py-2 text-left font-medium">Bridge Ports</th>
                <th className="w-20 px-3 py-2" />
              </tr>
            </thead>
            <tbody className="divide-y">
              {networkInterfaces.map((iface) => (
                editIface === iface.iface ? (
                  <tr key={iface.iface} className="bg-muted/20">
                    <td className="px-3 py-2 font-mono text-xs">{iface.iface}</td>
                    <td className="px-3 py-2">
                      <Badge variant="outline" className="text-xs">{iface.type}</Badge>
                    </td>
                    <td className="px-3 py-2">
                      <Badge variant={iface.active ? "default" : "secondary"} className="text-xs">
                        {iface.active ? "up" : "down"}
                      </Badge>
                    </td>
                    <td className="px-3 py-2">
                      <Input className="h-7 text-xs font-mono" value={editCidr} onChange={(e) => { setEditCidr(e.target.value); }} placeholder="CIDR" />
                    </td>
                    <td className="px-3 py-2">
                      <Input className="h-7 text-xs font-mono" value={editGateway} onChange={(e) => { setEditGateway(e.target.value); }} placeholder="Gateway" />
                    </td>
                    <td className="px-3 py-2">
                      <Input className="h-7 text-xs" value={editBridgePorts} onChange={(e) => { setEditBridgePorts(e.target.value); }} placeholder="Bridge ports" />
                    </td>
                    <td className="px-3 py-2">
                      <div className="flex gap-1">
                        <Button variant="ghost" size="icon" className="h-7 w-7" onClick={() => { saveEdit(iface.iface, iface.type); }} disabled={updateIface.isPending}>
                          <Check className="h-3.5 w-3.5 text-green-600" />
                        </Button>
                        <Button variant="ghost" size="icon" className="h-7 w-7" onClick={() => { setEditIface(null); }}>
                          <RotateCcw className="h-3.5 w-3.5" />
                        </Button>
                      </div>
                    </td>
                  </tr>
                ) : (
                  <tr key={iface.iface} className="hover:bg-muted/30">
                    <td className="px-3 py-2 font-mono text-xs">{iface.iface}</td>
                    <td className="px-3 py-2">
                      <Badge variant="outline" className="text-xs">{iface.type}</Badge>
                    </td>
                    <td className="px-3 py-2">
                      <Badge variant={iface.active ? "default" : "secondary"} className="text-xs">
                        {iface.active ? "up" : "down"}
                      </Badge>
                    </td>
                    <td className="px-3 py-2 font-mono text-xs">{iface.cidr || iface.address || "--"}</td>
                    <td className="px-3 py-2 font-mono text-xs">{iface.gateway || "--"}</td>
                    <td className="px-3 py-2 text-xs">{iface.bridge_ports || "--"}</td>
                    <td className="px-3 py-2">
                      <div className="flex gap-1">
                        <Button variant="ghost" size="icon" className="h-7 w-7" onClick={() => { openEdit(iface); }}>
                          <Pencil className="h-3.5 w-3.5" />
                        </Button>
                        <Button variant="ghost" size="icon" className="h-7 w-7" onClick={() => { deleteIface.mutate(iface.iface); }} disabled={deleteIface.isPending}>
                          <Trash2 className="h-3.5 w-3.5 text-destructive" />
                        </Button>
                      </div>
                    </td>
                  </tr>
                )
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

function DisksTab({ clusterId, nodeId, nodeName, isOnline }: { clusterId: string; nodeId: string; nodeName: string; isOnline: boolean }) {
  const { data: disks, isLoading } = useNodeDisks(clusterId, nodeId);
  const [smartDisk, setSmartDisk] = useState<string | null>(null);
  const initGPT = useInitializeGPT(clusterId, nodeName);
  const wipeDisk = useWipeDisk(clusterId, nodeName);

  return (
    <div className="space-y-6">
      {/* Physical Disks */}
      <div className="space-y-3">
        <div className="flex items-center gap-2">
          <HardDrive className="h-4 w-4 text-muted-foreground" />
          <h2 className="text-lg font-semibold">Physical Disks</h2>
        </div>
        {isLoading ? <Skeleton className="h-48 w-full" /> : !disks || disks.length === 0 ? (
          <p className="text-sm text-muted-foreground">No physical disks found.</p>
        ) : (
          <div className="overflow-x-auto rounded-lg border">
            <table className="w-full text-sm">
              <thead className="border-b bg-muted/50">
                <tr>
                  <th className="px-3 py-2 text-left font-medium">Device</th>
                  <th className="px-3 py-2 text-left font-medium">Model</th>
                  <th className="px-3 py-2 text-left font-medium">Serial</th>
                  <th className="px-3 py-2 text-left font-medium">Size</th>
                  <th className="px-3 py-2 text-left font-medium">Type</th>
                  <th className="px-3 py-2 text-left font-medium">Health</th>
                  <th className="px-3 py-2 text-left font-medium">Wearout</th>
                  {isOnline && <th className="w-28 px-3 py-2 text-left font-medium">Actions</th>}
                </tr>
              </thead>
              <tbody className="divide-y">
                {disks.map((d) => (
                  <tr key={d.id} className="hover:bg-muted/30">
                    <td className="px-3 py-2 font-mono text-xs">
                      <button
                        type="button"
                        className="text-primary underline-offset-4 hover:underline"
                        onClick={() => { setSmartDisk(smartDisk === d.dev_path ? null : d.dev_path); }}
                      >
                        {d.dev_path}
                      </button>
                    </td>
                    <td className="px-3 py-2">{d.model || "--"}</td>
                    <td className="px-3 py-2 font-mono text-xs">{d.serial || "--"}</td>
                    <td className="px-3 py-2">{formatBytes(d.size)}</td>
                    <td className="px-3 py-2">
                      <Badge variant="outline" className="text-xs">
                        {d.disk_type.toUpperCase() || "unknown"}
                      </Badge>
                    </td>
                    <td className="px-3 py-2">
                      <Badge variant={d.health === "PASSED" ? "default" : "destructive"} className="text-xs">
                        {d.health || "unknown"}
                      </Badge>
                    </td>
                    <td className="px-3 py-2">{d.wearout && d.wearout !== "N/A" ? d.wearout : "--"}</td>
                    {isOnline && (
                      <td className="px-3 py-2">
                        <div className="flex gap-1">
                          <AlertDialog>
                            <AlertDialogTrigger asChild>
                              <Button variant="outline" size="sm" className="h-7 text-xs">Init GPT</Button>
                            </AlertDialogTrigger>
                            <AlertDialogContent>
                              <AlertDialogHeader>
                                <AlertDialogTitle>Initialize {d.dev_path} with GPT?</AlertDialogTitle>
                                <AlertDialogDescription>
                                  This will create a new GPT partition table on the disk. All existing data will be lost.
                                </AlertDialogDescription>
                              </AlertDialogHeader>
                              <AlertDialogFooter>
                                <AlertDialogCancel>Cancel</AlertDialogCancel>
                                <AlertDialogAction
                                  onClick={() => { initGPT.mutate(d.dev_path); }}
                                  className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
                                >
                                  Initialize
                                </AlertDialogAction>
                              </AlertDialogFooter>
                            </AlertDialogContent>
                          </AlertDialog>
                          <AlertDialog>
                            <AlertDialogTrigger asChild>
                              <Button variant="outline" size="sm" className="h-7 text-xs">Wipe</Button>
                            </AlertDialogTrigger>
                            <AlertDialogContent>
                              <AlertDialogHeader>
                                <AlertDialogTitle>Wipe {d.dev_path}?</AlertDialogTitle>
                                <AlertDialogDescription>
                                  This will wipe all data from the disk. This action cannot be undone.
                                </AlertDialogDescription>
                              </AlertDialogHeader>
                              <AlertDialogFooter>
                                <AlertDialogCancel>Cancel</AlertDialogCancel>
                                <AlertDialogAction
                                  onClick={() => { wipeDisk.mutate(d.dev_path); }}
                                  className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
                                >
                                  Wipe
                                </AlertDialogAction>
                              </AlertDialogFooter>
                            </AlertDialogContent>
                          </AlertDialog>
                        </div>
                      </td>
                    )}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}

        {/* SMART detail panel */}
        {smartDisk && isOnline && (
          <DiskSMARTPanel clusterId={clusterId} nodeName={nodeName} disk={smartDisk} />
        )}
      </div>

      {/* ZFS / LVM / LVM-Thin / Directory sections (only when online) */}
      {isOnline && (
        <>
          <ZFSPoolsSection clusterId={clusterId} nodeName={nodeName} />
          <LVMSection clusterId={clusterId} nodeName={nodeName} />
          <LVMThinSection clusterId={clusterId} nodeName={nodeName} />
          <DirectorySection clusterId={clusterId} nodeName={nodeName} />
        </>
      )}
    </div>
  );
}

function DiskSMARTPanel({ clusterId, nodeName, disk }: { clusterId: string; nodeName: string; disk: string }) {
  const { data: smart, isLoading } = useDiskSMART(clusterId, nodeName, disk);

  if (isLoading) return <Skeleton className="h-24 w-full" />;
  if (!smart) return null;

  return (
    <div className="rounded-lg border p-4">
      <div className="mb-3 flex items-center justify-between">
        <h3 className="text-sm font-semibold">S.M.A.R.T. Data - {disk}</h3>
        <Badge variant={smart.health === "PASSED" ? "default" : "destructive"} className="text-xs">
          {smart.health}
        </Badge>
      </div>
      {smart.attributes && smart.attributes.length > 0 ? (
        <div className="overflow-x-auto">
          <table className="w-full text-xs">
            <thead className="border-b bg-muted/50">
              <tr>
                <th className="px-2 py-1 text-left">ID</th>
                <th className="px-2 py-1 text-left">Attribute</th>
                <th className="px-2 py-1 text-right">Value</th>
                <th className="px-2 py-1 text-right">Worst</th>
                <th className="px-2 py-1 text-right">Thresh</th>
                <th className="px-2 py-1 text-left">Raw</th>
              </tr>
            </thead>
            <tbody className="divide-y">
              {smart.attributes.map((attr) => (
                <tr key={attr.id} className={attr.value <= attr.threshold ? "bg-destructive/10" : ""}>
                  <td className="px-2 py-1 font-mono">{attr.id}</td>
                  <td className="px-2 py-1">{attr.name}</td>
                  <td className="px-2 py-1 text-right font-mono">{attr.value}</td>
                  <td className="px-2 py-1 text-right font-mono">{attr.worst}</td>
                  <td className="px-2 py-1 text-right font-mono">{attr.threshold}</td>
                  <td className="px-2 py-1 font-mono">{attr.raw}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : smart.text ? (
        <pre className="max-h-48 overflow-auto whitespace-pre-wrap rounded bg-muted p-2 text-xs">{smart.text}</pre>
      ) : (
        <p className="text-xs text-muted-foreground">No S.M.A.R.T. attributes available.</p>
      )}
    </div>
  );
}

function diskLabel(d: LiveDiskResponse): string {
  const size = formatBytes(d.size);
  const model = d.model ? ` - ${d.model}` : "";
  const used = d.used ? ` [${d.used}]` : " [unused]";
  return `${d.dev_path} (${size}${model})${used}`;
}

function ZFSPoolsSection({ clusterId, nodeName }: { clusterId: string; nodeName: string }) {
  const { data: pools, isLoading } = useNodeZFSPools(clusterId, nodeName);
  const { data: liveDisks } = useLiveDisks(clusterId, nodeName);
  const [showCreate, setShowCreate] = useState(false);
  const [zfsName, setZfsName] = useState("");
  const [zfsRaid, setZfsRaid] = useState("single");
  const [zfsSelectedDisks, setZfsSelectedDisks] = useState<string[]>([]);
  const [zfsCompression, setZfsCompression] = useState("on");
  const createZFS = useCreateZFSPool(clusterId, nodeName);
  const deleteZFS = useDeleteZFSPool(clusterId, nodeName);
  const deleteError = deleteZFS.error instanceof Error ? deleteZFS.error.message : "";
  const [cleanupDisks, setCleanupDisks] = useState(true);
  const [cleanupConfig, setCleanupConfig] = useState(true);

  const unusedDisks = liveDisks?.filter((d) => !d.used) ?? [];

  const toggleDisk = (devPath: string) => {
    setZfsSelectedDisks((prev) =>
      prev.includes(devPath) ? prev.filter((p) => p !== devPath) : [...prev, devPath],
    );
  };

  const handleCreate = () => {
    createZFS.mutate(
      { name: zfsName, raidlevel: zfsRaid, devices: zfsSelectedDisks.join(","), compression: zfsCompression },
      { onSuccess: () => { setShowCreate(false); setZfsName(""); setZfsSelectedDisks([]); } },
    );
  };

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between">
        <h2 className="text-lg font-semibold">ZFS Pools</h2>
        <Button variant="outline" size="sm" onClick={() => { setShowCreate(!showCreate); }}>
          <Plus className="mr-1 h-4 w-4" />Create ZFS
        </Button>
      </div>
      {showCreate && (
        <div className="rounded-lg border p-4 space-y-3">
          <div className="grid gap-3 sm:grid-cols-2">
            <div className="space-y-1">
              <Label className="text-xs">Pool Name</Label>
              <Input className="h-8 text-sm" value={zfsName} onChange={(e) => { setZfsName(e.target.value); }} placeholder="e.g. mypool" />
            </div>
            <div className="space-y-1">
              <Label className="text-xs">RAID Level</Label>
              <select className="h-8 w-full rounded-md border bg-background px-2 text-sm" value={zfsRaid} onChange={(e) => { setZfsRaid(e.target.value); }}>
                <option value="single">Single</option>
                <option value="mirror">Mirror</option>
                <option value="raidz">RAIDZ</option>
                <option value="raidz2">RAIDZ2</option>
                <option value="raidz3">RAIDZ3</option>
              </select>
            </div>
            <div className="space-y-1 sm:col-span-2">
              <Label className="text-xs">Select Disks ({zfsSelectedDisks.length} selected)</Label>
              {unusedDisks.length === 0 ? (
                <p className="text-xs text-muted-foreground">No unused disks available</p>
              ) : (
                <div className="max-h-40 overflow-auto rounded border p-2 space-y-1">
                  {unusedDisks.map((d) => (
                    <label key={d.dev_path} className="flex items-center gap-2 text-xs hover:bg-muted/30 rounded px-1 py-0.5 cursor-pointer">
                      <input
                        type="checkbox"
                        checked={zfsSelectedDisks.includes(d.dev_path)}
                        onChange={() => { toggleDisk(d.dev_path); }}
                        className="rounded border"
                      />
                      <span className="font-mono">{d.dev_path}</span>
                      <span className="text-muted-foreground">{formatBytes(d.size)}</span>
                      <span className="text-muted-foreground">{d.model || ""}</span>
                      <Badge variant="outline" className="text-[10px]">{d.disk_type.toUpperCase()}</Badge>
                    </label>
                  ))}
                </div>
              )}
            </div>
            <div className="space-y-1">
              <Label className="text-xs">Compression</Label>
              <select className="h-8 w-full rounded-md border bg-background px-2 text-sm" value={zfsCompression} onChange={(e) => { setZfsCompression(e.target.value); }}>
                <option value="on">On (LZ4)</option>
                <option value="off">Off</option>
                <option value="lz4">LZ4</option>
                <option value="zstd">ZSTD</option>
                <option value="gzip">GZIP</option>
              </select>
            </div>
          </div>
          <div className="flex justify-end gap-2">
            <Button variant="outline" size="sm" onClick={() => { setShowCreate(false); }}>Cancel</Button>
            <Button size="sm" onClick={handleCreate} disabled={!zfsName || zfsSelectedDisks.length === 0 || createZFS.isPending}>Create</Button>
          </div>
        </div>
      )}
      {isLoading ? <Skeleton className="h-24 w-full" /> : !pools || pools.length === 0 ? (
        <p className="text-sm text-muted-foreground">No ZFS pools found.</p>
      ) : (
        <div className="overflow-x-auto rounded-lg border">
          <table className="w-full text-sm">
            <thead className="border-b bg-muted/50">
              <tr>
                <th className="px-3 py-2 text-left font-medium">Name</th>
                <th className="px-3 py-2 text-left font-medium">Size</th>
                <th className="px-3 py-2 text-left font-medium">Allocated</th>
                <th className="px-3 py-2 text-left font-medium">Free</th>
                <th className="px-3 py-2 text-left font-medium">Frag</th>
                <th className="px-3 py-2 text-left font-medium">Health</th>
                <th className="px-3 py-2 text-right font-medium">Actions</th>
              </tr>
            </thead>
            <tbody className="divide-y">
              {pools.map((p: ZFSPoolResponse) => (
                <tr key={p.name} className="hover:bg-muted/30">
                  <td className="px-3 py-2 font-mono text-xs">{p.name}</td>
                  <td className="px-3 py-2">{formatBytes(p.size)}</td>
                  <td className="px-3 py-2">{formatBytes(p.alloc)}</td>
                  <td className="px-3 py-2">{formatBytes(p.free)}</td>
                  <td className="px-3 py-2">{p.frag}%</td>
                  <td className="px-3 py-2">
                    <Badge variant={p.health === "ONLINE" ? "default" : "destructive"} className="text-xs">
                      {p.health}
                    </Badge>
                  </td>
                  <td className="px-3 py-2 text-right">
                    <AlertDialog>
                      <AlertDialogTrigger asChild>
                        <Button variant="ghost" size="icon" className="h-7 w-7 text-destructive hover:text-destructive">
                          <Trash2 className="h-4 w-4" />
                        </Button>
                      </AlertDialogTrigger>
                      <AlertDialogContent>
                        <AlertDialogHeader>
                          <AlertDialogTitle>Destroy ZFS Pool</AlertDialogTitle>
                          <AlertDialogDescription>
                            Are you sure you want to destroy <span className="font-mono font-semibold">{p.name}</span>? This will permanently delete the pool and all data on it. This action cannot be undone.
                          </AlertDialogDescription>
                        </AlertDialogHeader>
                        <div className="space-y-2 py-2">
                          <label className="flex items-center gap-2 text-sm">
                            <input
                              type="checkbox"
                              checked={cleanupDisks}
                              onChange={(e) => { setCleanupDisks(e.target.checked); }}
                              className="rounded border"
                            />
                            Cleanup Disks
                          </label>
                          <p className="ml-6 text-xs text-muted-foreground">Wipe partition tables of member disks</p>
                          <label className="flex items-center gap-2 text-sm">
                            <input
                              type="checkbox"
                              checked={cleanupConfig}
                              onChange={(e) => { setCleanupConfig(e.target.checked); }}
                              className="rounded border"
                            />
                            Cleanup Storage Configuration
                          </label>
                          <p className="ml-6 text-xs text-muted-foreground">Remove associated storage from Proxmox configuration</p>
                        </div>
                        {deleteError && (
                          <p className="text-sm text-destructive">{deleteError}</p>
                        )}
                        <AlertDialogFooter>
                          <AlertDialogCancel>Cancel</AlertDialogCancel>
                          <AlertDialogAction
                            className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
                            disabled={deleteZFS.isPending}
                            onClick={() => { deleteZFS.mutate({ poolName: p.name, cleanupDisks, cleanupConfig }); }}
                          >
                            {deleteZFS.isPending ? "Destroying…" : "Destroy"}
                          </AlertDialogAction>
                        </AlertDialogFooter>
                      </AlertDialogContent>
                    </AlertDialog>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

function LVMSection({ clusterId, nodeName }: { clusterId: string; nodeName: string }) {
  const { data: vgs, isLoading } = useNodeLVM(clusterId, nodeName);
  const { data: liveDisks } = useLiveDisks(clusterId, nodeName);
  const [showCreate, setShowCreate] = useState(false);
  const [lvmName, setLvmName] = useState("");
  const [lvmDevice, setLvmDevice] = useState("");
  const [lvmAddStorage, setLvmAddStorage] = useState(true);
  const createLVM = useCreateLVM(clusterId, nodeName);
  const deleteLVM = useDeleteLVM(clusterId, nodeName);
  const deleteError = deleteLVM.error instanceof Error ? deleteLVM.error.message : "";
  const [lvmCleanupDisks, setLvmCleanupDisks] = useState(true);
  const [lvmCleanupConfig, setLvmCleanupConfig] = useState(true);
  const unusedDisks = liveDisks?.filter((d) => !d.used) ?? [];

  const handleCreate = () => {
    createLVM.mutate(
      { name: lvmName, device: lvmDevice, add_storage: lvmAddStorage },
      { onSuccess: () => { setShowCreate(false); setLvmName(""); setLvmDevice(""); } },
    );
  };

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between">
        <h2 className="text-lg font-semibold">LVM Volume Groups</h2>
        <Button variant="outline" size="sm" onClick={() => { setShowCreate(!showCreate); }}>
          <Plus className="mr-1 h-4 w-4" />Create LVM
        </Button>
      </div>
      {showCreate && (
        <div className="rounded-lg border p-4 space-y-3">
          <div className="grid gap-3 sm:grid-cols-2">
            <div className="space-y-1">
              <Label className="text-xs">Volume Group Name</Label>
              <Input className="h-8 text-sm" value={lvmName} onChange={(e) => { setLvmName(e.target.value); }} placeholder="e.g. myvg" />
            </div>
            <div className="space-y-1">
              <Label className="text-xs">Disk</Label>
              <select className="h-8 w-full rounded-md border bg-background px-2 text-sm" value={lvmDevice} onChange={(e) => { setLvmDevice(e.target.value); }}>
                <option value="">Select a disk...</option>
                {unusedDisks.map((d) => (
                  <option key={d.dev_path} value={d.dev_path}>{diskLabel(d)}</option>
                ))}
              </select>
            </div>
          </div>
          <div className="flex items-center gap-2">
            <input type="checkbox" id="lvm-add-storage" checked={lvmAddStorage} onChange={(e) => { setLvmAddStorage(e.target.checked); }} className="rounded border" />
            <Label htmlFor="lvm-add-storage" className="text-xs">Add as Proxmox storage</Label>
          </div>
          <div className="flex justify-end gap-2">
            <Button variant="outline" size="sm" onClick={() => { setShowCreate(false); }}>Cancel</Button>
            <Button size="sm" onClick={handleCreate} disabled={!lvmName || !lvmDevice || createLVM.isPending}>Create</Button>
          </div>
        </div>
      )}
      {isLoading ? <Skeleton className="h-24 w-full" /> : !vgs || vgs.length === 0 ? (
        <p className="text-sm text-muted-foreground">No LVM volume groups found.</p>
      ) : (
        <div className="overflow-x-auto rounded-lg border">
          <table className="w-full text-sm">
            <thead className="border-b bg-muted/50">
              <tr>
                <th className="px-3 py-2 text-left font-medium">Name</th>
                <th className="px-3 py-2 text-left font-medium">Size</th>
                <th className="px-3 py-2 text-left font-medium">Free</th>
                <th className="px-3 py-2 text-left font-medium">PVs</th>
                <th className="px-3 py-2 text-left font-medium">LVs</th>
                <th className="px-3 py-2 text-right font-medium">Actions</th>
              </tr>
            </thead>
            <tbody className="divide-y">
              {vgs.map((vg: LVMVolumeGroupResponse) => (
                <tr key={vg.name} className="hover:bg-muted/30">
                  <td className="px-3 py-2 font-mono text-xs">{vg.name}</td>
                  <td className="px-3 py-2">{formatBytes(vg.size)}</td>
                  <td className="px-3 py-2">{formatBytes(vg.free)}</td>
                  <td className="px-3 py-2">{vg.pv_count}</td>
                  <td className="px-3 py-2">{vg.lv_count}</td>
                  <td className="px-3 py-2 text-right">
                    <AlertDialog>
                      <AlertDialogTrigger asChild>
                        <Button variant="ghost" size="icon" className="h-7 w-7 text-destructive hover:text-destructive">
                          <Trash2 className="h-4 w-4" />
                        </Button>
                      </AlertDialogTrigger>
                      <AlertDialogContent>
                        <AlertDialogHeader>
                          <AlertDialogTitle>Destroy LVM Volume Group</AlertDialogTitle>
                          <AlertDialogDescription>
                            Are you sure you want to destroy <span className="font-mono font-semibold">{vg.name}</span>? This will permanently delete the volume group and all data on it. This action cannot be undone.
                          </AlertDialogDescription>
                        </AlertDialogHeader>
                        <div className="space-y-2 py-2">
                          <label className="flex items-center gap-2 text-sm">
                            <input type="checkbox" checked={lvmCleanupDisks} onChange={(e) => { setLvmCleanupDisks(e.target.checked); }} className="rounded border" />
                            Cleanup Disks
                          </label>
                          <p className="ml-6 text-xs text-muted-foreground">Wipe partition tables of member disks</p>
                          <label className="flex items-center gap-2 text-sm">
                            <input type="checkbox" checked={lvmCleanupConfig} onChange={(e) => { setLvmCleanupConfig(e.target.checked); }} className="rounded border" />
                            Cleanup Storage Configuration
                          </label>
                          <p className="ml-6 text-xs text-muted-foreground">Remove associated storage from Proxmox configuration</p>
                        </div>
                        {deleteError && (
                          <p className="text-sm text-destructive">{deleteError}</p>
                        )}
                        <AlertDialogFooter>
                          <AlertDialogCancel>Cancel</AlertDialogCancel>
                          <AlertDialogAction
                            className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
                            disabled={deleteLVM.isPending}
                            onClick={() => { deleteLVM.mutate({ name: vg.name, cleanupDisks: lvmCleanupDisks, cleanupConfig: lvmCleanupConfig }); }}
                          >
                            {deleteLVM.isPending ? "Destroying…" : "Destroy"}
                          </AlertDialogAction>
                        </AlertDialogFooter>
                      </AlertDialogContent>
                    </AlertDialog>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

function LVMThinSection({ clusterId, nodeName }: { clusterId: string; nodeName: string }) {
  const { data: pools, isLoading } = useNodeLVMThin(clusterId, nodeName);
  const { data: liveDisks } = useLiveDisks(clusterId, nodeName);
  const [showCreate, setShowCreate] = useState(false);
  const [thinName, setThinName] = useState("");
  const [thinDevice, setThinDevice] = useState("");
  const [thinAddStorage, setThinAddStorage] = useState(true);
  const createThin = useCreateLVMThin(clusterId, nodeName);
  const deleteThin = useDeleteLVMThin(clusterId, nodeName);
  const thinDeleteError = deleteThin.error instanceof Error ? deleteThin.error.message : "";
  const [thinCleanupDisks, setThinCleanupDisks] = useState(true);
  const [thinCleanupConfig, setThinCleanupConfig] = useState(true);
  const unusedDisks = liveDisks?.filter((d) => !d.used) ?? [];

  const handleCreate = () => {
    createThin.mutate(
      { name: thinName, device: thinDevice, add_storage: thinAddStorage },
      { onSuccess: () => { setShowCreate(false); setThinName(""); setThinDevice(""); } },
    );
  };

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between">
        <h2 className="text-lg font-semibold">LVM-Thin Pools</h2>
        <Button variant="outline" size="sm" onClick={() => { setShowCreate(!showCreate); }}>
          <Plus className="mr-1 h-4 w-4" />Create LVM-Thin
        </Button>
      </div>
      {showCreate && (
        <div className="rounded-lg border p-4 space-y-3">
          <div className="grid gap-3 sm:grid-cols-2">
            <div className="space-y-1">
              <Label className="text-xs">Thin Pool Name</Label>
              <Input className="h-8 text-sm" value={thinName} onChange={(e) => { setThinName(e.target.value); }} placeholder="e.g. mythinpool" />
            </div>
            <div className="space-y-1">
              <Label className="text-xs">Disk</Label>
              <select className="h-8 w-full rounded-md border bg-background px-2 text-sm" value={thinDevice} onChange={(e) => { setThinDevice(e.target.value); }}>
                <option value="">Select a disk...</option>
                {unusedDisks.map((d) => (
                  <option key={d.dev_path} value={d.dev_path}>{diskLabel(d)}</option>
                ))}
              </select>
            </div>
          </div>
          <div className="flex items-center gap-2">
            <input type="checkbox" id="thin-add-storage" checked={thinAddStorage} onChange={(e) => { setThinAddStorage(e.target.checked); }} className="rounded border" />
            <Label htmlFor="thin-add-storage" className="text-xs">Add as Proxmox storage</Label>
          </div>
          <div className="flex justify-end gap-2">
            <Button variant="outline" size="sm" onClick={() => { setShowCreate(false); }}>Cancel</Button>
            <Button size="sm" onClick={handleCreate} disabled={!thinName || !thinDevice || createThin.isPending}>Create</Button>
          </div>
        </div>
      )}
      {isLoading ? <Skeleton className="h-24 w-full" /> : !pools || pools.length === 0 ? (
        <p className="text-sm text-muted-foreground">No LVM thin pools found.</p>
      ) : (
        <div className="overflow-x-auto rounded-lg border">
          <table className="w-full text-sm">
            <thead className="border-b bg-muted/50">
              <tr>
                <th className="px-3 py-2 text-left font-medium">LV</th>
                <th className="px-3 py-2 text-left font-medium">VG</th>
                <th className="px-3 py-2 text-left font-medium">Size</th>
                <th className="px-3 py-2 text-left font-medium">Used</th>
                <th className="px-3 py-2 text-left font-medium">Data %</th>
                <th className="px-3 py-2 text-right font-medium">Actions</th>
              </tr>
            </thead>
            <tbody className="divide-y">
              {pools.map((p: LVMThinPoolResponse) => (
                <tr key={`${p.vg}-${p.lv}`} className="hover:bg-muted/30">
                  <td className="px-3 py-2 font-mono text-xs">{p.lv}</td>
                  <td className="px-3 py-2 font-mono text-xs">{p.vg}</td>
                  <td className="px-3 py-2">{formatBytes(p.lv_size)}</td>
                  <td className="px-3 py-2">{formatBytes(p.used)}</td>
                  <td className="px-3 py-2">{p.data_percent.toFixed(1)}%</td>
                  <td className="px-3 py-2 text-right">
                    <AlertDialog>
                      <AlertDialogTrigger asChild>
                        <Button variant="ghost" size="icon" className="h-7 w-7 text-destructive hover:text-destructive">
                          <Trash2 className="h-4 w-4" />
                        </Button>
                      </AlertDialogTrigger>
                      <AlertDialogContent>
                        <AlertDialogHeader>
                          <AlertDialogTitle>Destroy LVM-Thin Pool</AlertDialogTitle>
                          <AlertDialogDescription>
                            Are you sure you want to destroy <span className="font-mono font-semibold">{p.vg}/{p.lv}</span>? This will permanently delete the thin pool and all data on it. This action cannot be undone.
                          </AlertDialogDescription>
                        </AlertDialogHeader>
                        <div className="space-y-2 py-2">
                          <label className="flex items-center gap-2 text-sm">
                            <input type="checkbox" checked={thinCleanupDisks} onChange={(e) => { setThinCleanupDisks(e.target.checked); }} className="rounded border" />
                            Cleanup Disks
                          </label>
                          <p className="ml-6 text-xs text-muted-foreground">Wipe partition tables of member disks</p>
                          <label className="flex items-center gap-2 text-sm">
                            <input type="checkbox" checked={thinCleanupConfig} onChange={(e) => { setThinCleanupConfig(e.target.checked); }} className="rounded border" />
                            Cleanup Storage Configuration
                          </label>
                          <p className="ml-6 text-xs text-muted-foreground">Remove associated storage from Proxmox configuration</p>
                        </div>
                        {thinDeleteError && (
                          <p className="text-sm text-destructive">{thinDeleteError}</p>
                        )}
                        <AlertDialogFooter>
                          <AlertDialogCancel>Cancel</AlertDialogCancel>
                          <AlertDialogAction
                            className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
                            disabled={deleteThin.isPending}
                            onClick={() => { deleteThin.mutate({ name: `${p.vg}-${p.lv}`, cleanupDisks: thinCleanupDisks, cleanupConfig: thinCleanupConfig }); }}
                          >
                            {deleteThin.isPending ? "Destroying…" : "Destroy"}
                          </AlertDialogAction>
                        </AlertDialogFooter>
                      </AlertDialogContent>
                    </AlertDialog>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

function DirectorySection({ clusterId, nodeName }: { clusterId: string; nodeName: string }) {
  const { data: dirs, isLoading } = useNodeDirectories(clusterId, nodeName);
  const { data: liveDisks } = useLiveDisks(clusterId, nodeName);
  const [showCreate, setShowCreate] = useState(false);
  const [dirName, setDirName] = useState("");
  const [dirDevice, setDirDevice] = useState("");
  const [dirFs, setDirFs] = useState("ext4");
  const [dirAddStorage, setDirAddStorage] = useState(true);
  const createDir = useCreateDirectory(clusterId, nodeName);
  const unusedDisks = liveDisks?.filter((d) => !d.used) ?? [];

  const handleCreate = () => {
    createDir.mutate(
      { name: dirName, device: dirDevice, filesystem: dirFs, add_storage: dirAddStorage },
      { onSuccess: () => { setShowCreate(false); setDirName(""); setDirDevice(""); } },
    );
  };

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between">
        <h2 className="text-lg font-semibold">Directory Storage</h2>
        <Button variant="outline" size="sm" onClick={() => { setShowCreate(!showCreate); }}>
          <Plus className="mr-1 h-4 w-4" />Create Directory
        </Button>
      </div>
      {showCreate && (
        <div className="rounded-lg border p-4 space-y-3">
          <div className="grid gap-3 sm:grid-cols-2">
            <div className="space-y-1">
              <Label className="text-xs">Name</Label>
              <Input className="h-8 text-sm" value={dirName} onChange={(e) => { setDirName(e.target.value); }} placeholder="e.g. mydir" />
            </div>
            <div className="space-y-1">
              <Label className="text-xs">Disk</Label>
              <select className="h-8 w-full rounded-md border bg-background px-2 text-sm" value={dirDevice} onChange={(e) => { setDirDevice(e.target.value); }}>
                <option value="">Select a disk...</option>
                {unusedDisks.map((d) => (
                  <option key={d.dev_path} value={d.dev_path}>{diskLabel(d)}</option>
                ))}
              </select>
            </div>
            <div className="space-y-1">
              <Label className="text-xs">Filesystem</Label>
              <select className="h-8 w-full rounded-md border bg-background px-2 text-sm" value={dirFs} onChange={(e) => { setDirFs(e.target.value); }}>
                <option value="ext4">ext4</option>
                <option value="xfs">XFS</option>
              </select>
            </div>
          </div>
          <div className="flex items-center gap-2">
            <input type="checkbox" id="dir-add-storage" checked={dirAddStorage} onChange={(e) => { setDirAddStorage(e.target.checked); }} className="rounded border" />
            <Label htmlFor="dir-add-storage" className="text-xs">Add as Proxmox storage</Label>
          </div>
          <div className="flex justify-end gap-2">
            <Button variant="outline" size="sm" onClick={() => { setShowCreate(false); }}>Cancel</Button>
            <Button size="sm" onClick={handleCreate} disabled={!dirName || !dirDevice || createDir.isPending}>Create</Button>
          </div>
        </div>
      )}
      {isLoading ? <Skeleton className="h-24 w-full" /> : !dirs || dirs.length === 0 ? (
        <p className="text-sm text-muted-foreground">No directory storage entries found.</p>
      ) : (
        <div className="overflow-x-auto rounded-lg border">
          <table className="w-full text-sm">
            <thead className="border-b bg-muted/50">
              <tr>
                <th className="px-3 py-2 text-left font-medium">Path</th>
                <th className="px-3 py-2 text-left font-medium">Device</th>
                <th className="px-3 py-2 text-left font-medium">Type</th>
                <th className="px-3 py-2 text-left font-medium">Options</th>
              </tr>
            </thead>
            <tbody className="divide-y">
              {dirs.map((d: DirectoryEntryResponse) => (
                <tr key={d.path} className="hover:bg-muted/30">
                  <td className="px-3 py-2 font-mono text-xs">{d.path}</td>
                  <td className="px-3 py-2 font-mono text-xs">{d.device}</td>
                  <td className="px-3 py-2 text-xs">{d.type}</td>
                  <td className="px-3 py-2 text-xs">{d.options || "--"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

function FirewallTab({ clusterId, nodeName }: { clusterId: string; nodeName: string }) {
  const [showLog, setShowLog] = useState(false);
  const { data: rules, isLoading } = useNodeFirewallRules(clusterId, nodeName);
  const deleteRule = useDeleteNodeFirewallRule(clusterId, nodeName);
  const createRule = useCreateNodeFirewallRule(clusterId, nodeName);
  const { data: logEntries, isLoading: logLoading } = useNodeFirewallLog(clusterId, nodeName);

  const handleQuickAdd = (action: string) => {
    createRule.mutate({
      type: "in",
      action,
      enable: 1,
    });
  };

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h2 className="text-lg font-semibold">Firewall Rules</h2>
        <div className="flex gap-2">
          <Button variant="outline" size="sm" onClick={() => { handleQuickAdd("ACCEPT"); }} disabled={createRule.isPending}>
            + Accept Rule
          </Button>
          <Button variant="outline" size="sm" onClick={() => { handleQuickAdd("DROP"); }} disabled={createRule.isPending}>
            + Drop Rule
          </Button>
          <Button variant={showLog ? "default" : "outline"} size="sm" onClick={() => { setShowLog(!showLog); }}>
            {showLog ? "Hide Log" : "Show Log"}
          </Button>
        </div>
      </div>

      {isLoading ? <Skeleton className="h-32 w-full" /> : !rules || rules.length === 0 ? (
        <p className="text-sm text-muted-foreground">No firewall rules configured.</p>
      ) : (
        <div className="overflow-x-auto rounded-lg border">
          <table className="w-full text-sm">
            <thead className="border-b bg-muted/50">
              <tr>
                <th className="px-3 py-2 text-left font-medium">#</th>
                <th className="px-3 py-2 text-left font-medium">Type</th>
                <th className="px-3 py-2 text-left font-medium">Action</th>
                <th className="px-3 py-2 text-left font-medium">Macro</th>
                <th className="px-3 py-2 text-left font-medium">Proto</th>
                <th className="px-3 py-2 text-left font-medium">Source</th>
                <th className="px-3 py-2 text-left font-medium">Dest</th>
                <th className="px-3 py-2 text-left font-medium">Port</th>
                <th className="px-3 py-2 text-left font-medium">Enabled</th>
                <th className="px-3 py-2 text-left font-medium">Comment</th>
                <th className="w-12 px-3 py-2" />
              </tr>
            </thead>
            <tbody className="divide-y">
              {rules.map((rule) => (
                <tr key={rule.pos} className="hover:bg-muted/30">
                  <td className="px-3 py-2 font-mono text-xs">{rule.pos}</td>
                  <td className="px-3 py-2 text-xs">{rule.type}</td>
                  <td className="px-3 py-2">
                    <Badge variant={rule.action === "ACCEPT" ? "default" : "destructive"} className="text-xs">
                      {rule.action}
                    </Badge>
                  </td>
                  <td className="px-3 py-2 text-xs">{rule.macro || "--"}</td>
                  <td className="px-3 py-2 text-xs">{rule.proto || "--"}</td>
                  <td className="px-3 py-2 font-mono text-xs">{rule.source || "--"}</td>
                  <td className="px-3 py-2 font-mono text-xs">{rule.dest || "--"}</td>
                  <td className="px-3 py-2 font-mono text-xs">{rule.dport || rule.sport || "--"}</td>
                  <td className="px-3 py-2">
                    <Badge variant={rule.enable ? "default" : "secondary"} className="text-xs">
                      {rule.enable ? "yes" : "no"}
                    </Badge>
                  </td>
                  <td className="px-3 py-2 text-xs">{rule.comment || "--"}</td>
                  <td className="px-3 py-2">
                    <Button
                      variant="ghost"
                      size="icon"
                      className="h-7 w-7"
                      onClick={() => { deleteRule.mutate(rule.pos); }}
                      disabled={deleteRule.isPending}
                    >
                      <Trash2 className="h-3.5 w-3.5 text-destructive" />
                    </Button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {showLog && (
        <div className="space-y-2">
          <h3 className="text-sm font-semibold">Firewall Log</h3>
          {logLoading ? <Skeleton className="h-32 w-full" /> : !logEntries || logEntries.length === 0 ? (
            <p className="text-xs text-muted-foreground">No log entries.</p>
          ) : (
            <div className="max-h-[400px] overflow-auto rounded-lg border bg-muted/30 p-3">
              <pre className="text-xs leading-relaxed">
                {logEntries.map((entry) => (
                  <div key={entry.n}>{entry.t}</div>
                ))}
              </pre>
            </div>
          )}
        </div>
      )}
    </div>
  );
}

function ServicesTab({ clusterId, nodeName }: { clusterId: string; nodeName: string }) {
  const { data: services, isLoading } = useNodeServices(clusterId, nodeName);
  const serviceAction = useServiceAction(clusterId, nodeName);

  if (isLoading) return <Skeleton className="h-48 w-full" />;

  if (!services || services.length === 0) {
    return <p className="text-sm text-muted-foreground">No services found.</p>;
  }

  return (
    <div className="space-y-3">
      <div className="flex items-center gap-2">
        <Cog className="h-4 w-4 text-muted-foreground" />
        <h2 className="text-lg font-semibold">Node Services</h2>
      </div>
      <div className="overflow-x-auto rounded-lg border">
        <table className="w-full text-sm">
          <thead className="border-b bg-muted/50">
            <tr>
              <th className="px-3 py-2 text-left font-medium">Service</th>
              <th className="px-3 py-2 text-left font-medium">Description</th>
              <th className="px-3 py-2 text-left font-medium">State</th>
              <th className="w-40 px-3 py-2 text-left font-medium">Actions</th>
            </tr>
          </thead>
          <tbody className="divide-y">
            {services.map((svc) => (
              <tr key={svc.service} className="hover:bg-muted/30">
                <td className="px-3 py-2 font-mono text-xs">{svc.service}</td>
                <td className="px-3 py-2 text-xs">{svc.desc || svc.name || "--"}</td>
                <td className="px-3 py-2">
                  <Badge variant={svc.state === "running" ? "default" : "secondary"} className="text-xs">
                    {svc.state}
                  </Badge>
                </td>
                <td className="px-3 py-2">
                  <div className="flex gap-1">
                    {svc.state !== "running" && (
                      <Button
                        variant="outline"
                        size="sm"
                        className="h-7 gap-1 text-xs"
                        onClick={() => { serviceAction.mutate({ service: svc.service, action: "start" }); }}
                        disabled={serviceAction.isPending}
                      >
                        <Play className="h-3 w-3" />
                        Start
                      </Button>
                    )}
                    {svc.state === "running" && (
                      <>
                        <Button
                          variant="outline"
                          size="sm"
                          className="h-7 gap-1 text-xs"
                          onClick={() => { serviceAction.mutate({ service: svc.service, action: "restart" }); }}
                          disabled={serviceAction.isPending}
                        >
                          <RotateCw className="h-3 w-3" />
                          Restart
                        </Button>
                        <Button
                          variant="outline"
                          size="sm"
                          className="h-7 gap-1 text-xs"
                          onClick={() => { serviceAction.mutate({ service: svc.service, action: "stop" }); }}
                          disabled={serviceAction.isPending}
                        >
                          <Square className="h-3 w-3" />
                          Stop
                        </Button>
                      </>
                    )}
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function SyslogTab({ clusterId, nodeName }: { clusterId: string; nodeName: string }) {
  const [serviceFilter, setServiceFilter] = useState("");
  const { data: entries, isLoading, isError } = useNodeSyslog(clusterId, nodeName, {
    limit: 200,
    service: serviceFilter || undefined,
  });

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2">
          <FileText className="h-4 w-4 text-muted-foreground" />
          <h2 className="text-lg font-semibold">System Log</h2>
        </div>
        <Input
          className="w-48"
          placeholder="Filter by service..."
          value={serviceFilter}
          onChange={(e) => { setServiceFilter(e.target.value); }}
        />
      </div>
      {isLoading ? <Skeleton className="h-64 w-full" /> : isError ? (
        <p className="text-sm text-destructive">Failed to load syslog. The node may be unreachable or the request timed out.</p>
      ) : !entries || entries.length === 0 ? (
        <p className="text-sm text-muted-foreground">No syslog entries found.</p>
      ) : (
        <div className="max-h-[600px] overflow-auto rounded-lg border bg-muted/30 p-3">
          <pre className="text-xs leading-relaxed">
            {entries.map((entry) => (
              <div key={entry.n} className="hover:bg-muted/50">
                {entry.t}
              </div>
            ))}
          </pre>
        </div>
      )}
    </div>
  );
}

function PCIDevicesTab({ clusterId, nodeId }: { clusterId: string; nodeId: string }) {
  const { data: pciDevices, isLoading } = useNodePCIDevices(clusterId, nodeId);

  if (isLoading) return <Skeleton className="h-48 w-full" />;

  if (!pciDevices || pciDevices.length === 0) {
    return <p className="text-sm text-muted-foreground">No PCI devices found.</p>;
  }

  return (
    <div className="space-y-3">
      <div className="flex items-center gap-2">
        <CircuitBoard className="h-4 w-4 text-muted-foreground" />
        <h2 className="text-lg font-semibold">PCI Devices</h2>
      </div>
      <div className="overflow-x-auto rounded-lg border">
        <table className="w-full text-sm">
          <thead className="border-b bg-muted/50">
            <tr>
              <th className="px-3 py-2 text-left font-medium">PCI ID</th>
              <th className="px-3 py-2 text-left font-medium">Device</th>
              <th className="px-3 py-2 text-left font-medium">Vendor</th>
              <th className="px-3 py-2 text-left font-medium">IOMMU Group</th>
            </tr>
          </thead>
          <tbody className="divide-y">
            {pciDevices.map((d) => (
              <tr key={d.id} className="hover:bg-muted/30">
                <td className="px-3 py-2 font-mono text-xs">{d.pci_id}</td>
                <td className="px-3 py-2 text-xs">{d.device_name || d.device || "--"}</td>
                <td className="px-3 py-2 text-xs">{d.vendor_name || d.vendor || "--"}</td>
                <td className="px-3 py-2">{d.iommu_group >= 0 ? String(d.iommu_group) : "--"}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

/* ------------------------------------------------------------------ */
/* Shared sub-components                                              */
/* ------------------------------------------------------------------ */

interface HardwareItem {
  label: string;
  value: string;
}

function HardwareSection({
  icon,
  title,
  items,
}: {
  icon: React.ReactNode;
  title: string;
  items: HardwareItem[];
}) {
  return (
    <div className="rounded-lg border p-4">
      <div className="mb-3 flex items-center gap-2 text-sm font-medium">
        <span className="text-muted-foreground">{icon}</span>
        {title}
      </div>
      <dl className="space-y-1.5">
        {items.map((item) => (
          <div key={item.label} className="flex justify-between gap-2 text-sm">
            <dt className="text-muted-foreground">{item.label}</dt>
            <dd className="truncate font-medium text-right" title={item.value}>{item.value}</dd>
          </div>
        ))}
      </dl>
    </div>
  );
}

function HardwareSectionWithAction({
  icon,
  title,
  items,
  action,
}: {
  icon: React.ReactNode;
  title: string;
  items: HardwareItem[];
  action?: React.ReactNode;
}) {
  return (
    <div className="rounded-lg border p-4">
      <div className="mb-3 flex items-center justify-between text-sm font-medium">
        <div className="flex items-center gap-2">
          <span className="text-muted-foreground">{icon}</span>
          {title}
        </div>
        {action}
      </div>
      <dl className="space-y-1.5">
        {items.map((item) => (
          <div key={item.label} className="flex justify-between gap-2 text-sm">
            <dt className="text-muted-foreground">{item.label}</dt>
            <dd className="truncate font-medium text-right" title={item.value}>{item.value}</dd>
          </div>
        ))}
      </dl>
    </div>
  );
}

function EditDNSDialog({ clusterId, nodeName }: { clusterId: string; nodeName: string }) {
  const [open, setOpen] = useState(false);
  const { data: dns } = useNodeDNS(clusterId, nodeName);
  const [search, setSearch] = useState("");
  const [dns1, setDns1] = useState("");
  const [dns2, setDns2] = useState("");
  const [dns3, setDns3] = useState("");
  const setNodeDNS = useSetNodeDNS(clusterId, nodeName);

  const handleOpen = (isOpen: boolean) => {
    if (isOpen && dns) {
      setSearch(dns.search);
      setDns1(dns.dns1);
      setDns2(dns.dns2);
      setDns3(dns.dns3);
    }
    setOpen(isOpen);
  };

  const handleSave = () => {
    setNodeDNS.mutate(
      { search, dns1, dns2, dns3 },
      { onSuccess: () => { setOpen(false); } },
    );
  };

  return (
    <Dialog open={open} onOpenChange={handleOpen}>
      <DialogTrigger asChild>
        <Button variant="ghost" size="icon" className="h-6 w-6">
          <Pencil className="h-3 w-3" />
        </Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Edit DNS Configuration - {nodeName}</DialogTitle>
        </DialogHeader>
        <div className="space-y-4">
          <div className="space-y-2">
            <Label>Search Domain</Label>
            <Input value={search} onChange={(e) => { setSearch(e.target.value); }} placeholder="e.g. example.com" />
          </div>
          <div className="space-y-2">
            <Label>DNS Server 1</Label>
            <Input value={dns1} onChange={(e) => { setDns1(e.target.value); }} placeholder="e.g. 8.8.8.8" />
          </div>
          <div className="space-y-2">
            <Label>DNS Server 2</Label>
            <Input value={dns2} onChange={(e) => { setDns2(e.target.value); }} placeholder="e.g. 8.8.4.4" />
          </div>
          <div className="space-y-2">
            <Label>DNS Server 3</Label>
            <Input value={dns3} onChange={(e) => { setDns3(e.target.value); }} placeholder="Optional" />
          </div>
          <div className="flex justify-end gap-2">
            <Button variant="outline" onClick={() => { setOpen(false); }}>Cancel</Button>
            <Button onClick={handleSave} disabled={!search || setNodeDNS.isPending}>Save</Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}

function EditTimezoneDialog({ clusterId, nodeName, currentTimezone }: { clusterId: string; nodeName: string; currentTimezone: string }) {
  const [open, setOpen] = useState(false);
  const [timezone, setTimezone] = useState(currentTimezone || "UTC");
  const setNodeTimezone = useSetNodeTimezone(clusterId, nodeName);

  const handleOpen = (isOpen: boolean) => {
    if (isOpen) {
      setTimezone(currentTimezone || "UTC");
    }
    setOpen(isOpen);
  };

  const handleSave = () => {
    setNodeTimezone.mutate(
      { timezone },
      { onSuccess: () => { setOpen(false); } },
    );
  };

  return (
    <Dialog open={open} onOpenChange={handleOpen}>
      <DialogTrigger asChild>
        <Button variant="ghost" size="icon" className="h-6 w-6">
          <Pencil className="h-3 w-3" />
        </Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Edit Timezone - {nodeName}</DialogTitle>
        </DialogHeader>
        <div className="space-y-4">
          <div className="space-y-2">
            <Label>Timezone</Label>
            <Input value={timezone} onChange={(e) => { setTimezone(e.target.value); }} placeholder="e.g. America/New_York" />
            <p className="text-xs text-muted-foreground">Enter an IANA timezone (e.g. UTC, America/New_York, Europe/London)</p>
          </div>
          <div className="flex justify-end gap-2">
            <Button variant="outline" onClick={() => { setOpen(false); }}>Cancel</Button>
            <Button onClick={handleSave} disabled={!timezone || setNodeTimezone.isPending}>Save</Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}

function NodePowerActions({ clusterId, nodeName, otherNodes }: { clusterId: string; nodeName: string; otherNodes: string[] }) {
  const shutdown = useShutdownNode(clusterId, nodeName);
  const reboot = useRebootNode(clusterId, nodeName);
  const evacuate = useEvacuateNode(clusterId, nodeName);
  const setPanelOpen = useTaskLogStore((s) => s.setPanelOpen);
  const setFocusedTask = useTaskLogStore((s) => s.setFocusedTask);
  const [actionError, setActionError] = useState<string | null>(null);
  const [evacuateOpen, setEvacuateOpen] = useState(false);
  const [mode, setMode] = useState<"distribute" | "single">("distribute");
  const [targetNode, setTargetNode] = useState("");
  const [migrations, setMigrations] = useState<EvacuateMigration[] | null>(null);

  const handleError = (err: unknown) => {
    const msg = err instanceof Error ? err.message : "Unknown error";
    setActionError(msg);
  };

  const handleEvacuate = () => {
    if (mode === "single" && !targetNode) return;
    setActionError(null);
    setMigrations(null);
    const params = mode === "single" ? { target_node: targetNode } : {};
    evacuate.mutate(params, {
      onSuccess: (data) => {
        setMigrations(data.migrations);
        // Focus the first successful migration task.
        const first = data.migrations.find((m) => m.upid && !m.error);
        if (first) {
          setFocusedTask({ clusterId, upid: first.upid, description: `Evacuate ${nodeName}` });
          setPanelOpen(true);
        }
      },
      onError: handleError,
    });
  };

  const closeEvacuate = () => {
    setEvacuateOpen(false);
    setTargetNode("");
    setMode("distribute");
    setMigrations(null);
  };

  return (
    <>
      {actionError && (
        <div className="flex items-center gap-2 rounded-md border border-destructive bg-destructive/10 px-3 py-1.5 text-sm text-destructive">
          <span className="flex-1">{actionError}</span>
          <Button variant="ghost" size="sm" className="h-6 px-2 text-xs" onClick={() => { setActionError(null); }}>Dismiss</Button>
        </div>
      )}
      <Dialog open={evacuateOpen} onOpenChange={(v) => { if (!v) closeEvacuate(); else setEvacuateOpen(true); }}>
        <DialogTrigger asChild>
          <Button variant="outline" size="sm" className="gap-1.5" disabled={otherNodes.length === 0}>
            <ArrowRightLeft className="h-4 w-4" />
            Evacuate
          </Button>
        </DialogTrigger>
        <DialogContent className="max-w-lg">
          <DialogHeader>
            <DialogTitle>Evacuate all guests from {nodeName}</DialogTitle>
          </DialogHeader>

          {!migrations ? (
            <>
              <p className="text-sm text-muted-foreground">
                Migrate all VMs and containers off this node.
              </p>
              <div className="space-y-3">
                <div className="space-y-2">
                  <Label>Distribution Mode</Label>
                  <div className="flex gap-3">
                    <label className="flex items-center gap-2 text-sm">
                      <input type="radio" name="evac-mode" checked={mode === "distribute"} onChange={() => { setMode("distribute"); setTargetNode(""); }} />
                      Distribute across nodes (DRS-aware)
                    </label>
                    <label className="flex items-center gap-2 text-sm">
                      <input type="radio" name="evac-mode" checked={mode === "single"} onChange={() => { setMode("single"); }} />
                      Single target
                    </label>
                  </div>
                </div>
                {mode === "single" && (
                  <div className="space-y-2">
                    <Label>Target Node</Label>
                    <select
                      className="flex h-9 w-full rounded-md border border-input bg-background px-3 py-1 text-sm shadow-sm transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
                      value={targetNode}
                      onChange={(e) => { setTargetNode(e.target.value); }}
                    >
                      <option value="">Select a target node...</option>
                      {otherNodes.map((n) => (
                        <option key={n} value={n}>{n}</option>
                      ))}
                    </select>
                  </div>
                )}
              </div>
              <div className="flex justify-end gap-2 pt-2">
                <Button variant="outline" onClick={closeEvacuate}>Cancel</Button>
                <Button onClick={handleEvacuate} disabled={(mode === "single" && !targetNode) || evacuate.isPending}>
                  {evacuate.isPending ? "Evacuating…" : "Evacuate"}
                </Button>
              </div>
            </>
          ) : (
            <>
              <div className="space-y-2">
                <p className="text-sm font-medium">{migrations.length} guest{migrations.length !== 1 ? "s" : ""} migrated:</p>
                <div className="max-h-64 space-y-1 overflow-auto">
                  {migrations.map((m) => (
                    <div
                      key={m.vmid}
                      className={`flex items-center justify-between rounded px-2 py-1 text-sm ${m.error ? "bg-destructive/10 text-destructive" : "bg-muted"}`}
                    >
                      <span className="font-medium">{m.name} <span className="text-muted-foreground">({m.type === "lxc" ? "CT" : "VM"} {String(m.vmid)})</span></span>
                      {m.error ? (
                        <span className="text-xs">{m.error}</span>
                      ) : (
                        <button
                          type="button"
                          className="text-xs text-primary hover:underline"
                          onClick={() => { setFocusedTask({ clusterId, upid: m.upid, description: `Migrate ${m.name}` }); setPanelOpen(true); }}
                        >
                          → {m.target_node}
                        </button>
                      )}
                    </div>
                  ))}
                </div>
              </div>
              <div className="flex justify-end pt-2">
                <Button variant="outline" onClick={closeEvacuate}>Close</Button>
              </div>
            </>
          )}
        </DialogContent>
      </Dialog>

      <AlertDialog>
        <AlertDialogTrigger asChild>
          <Button variant="outline" size="sm" className="gap-1.5">
            <RefreshCw className="h-4 w-4" />
            Reboot
          </Button>
        </AlertDialogTrigger>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Reboot {nodeName}?</AlertDialogTitle>
            <AlertDialogDescription>
              This will reboot the node. All running guests will be affected if not migrated first.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              onClick={() => { setActionError(null); reboot.mutate(undefined, { onError: handleError }); }}
              disabled={reboot.isPending}
            >
              Reboot
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <AlertDialog>
        <AlertDialogTrigger asChild>
          <Button variant="destructive" size="sm" className="gap-1.5">
            <Power className="h-4 w-4" />
            Shutdown
          </Button>
        </AlertDialogTrigger>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Shutdown {nodeName}?</AlertDialogTitle>
            <AlertDialogDescription>
              This will shut down the node. It will go offline and all running guests will be stopped. You will need physical or out-of-band access to power it back on.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              onClick={() => { setActionError(null); shutdown.mutate(undefined, { onError: handleError }); }}
              disabled={shutdown.isPending}
              className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
            >
              Shutdown
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  );
}

function NodeMetricsPanel({
  clusterId,
  nodeId,
  liveMetric,
}: {
  clusterId: string;
  nodeId: string;
  liveMetric: { cpuPercent: number; memPercent: number } | undefined;
}) {
  const [timeRange, setTimeRange] = useState<TimeRange>("1h");
  const { data: historicalData, isLoading } = useNodeHistoricalMetrics(clusterId, nodeId, timeRange);
  const chartData = historicalData ?? [];

  return (
    <div className="space-y-4">
      {/* Live gauges */}
      <div className="grid gap-4 sm:grid-cols-2">
        <div className="rounded-lg border p-4">
          <p className="mb-2 text-sm font-medium text-muted-foreground">CPU Usage (Live)</p>
          <MetricMiniBar value={liveMetric?.cpuPercent ?? null} />
          <p className="mt-1 text-xs text-muted-foreground">
            {liveMetric ? `${liveMetric.cpuPercent.toFixed(1)}%` : "No live data"}
          </p>
        </div>
        <div className="rounded-lg border p-4">
          <p className="mb-2 text-sm font-medium text-muted-foreground">Memory Usage (Live)</p>
          <MetricMiniBar value={liveMetric?.memPercent ?? null} />
          <p className="mt-1 text-xs text-muted-foreground">
            {liveMetric ? `${liveMetric.memPercent.toFixed(1)}%` : "No live data"}
          </p>
        </div>
      </div>

      {/* Time range selector */}
      <div className="flex items-center gap-2">
        <span className="text-sm font-medium text-muted-foreground">Historical:</span>
        <div className="flex gap-1">
          {TIME_RANGES.map((tr) => (
            <Button
              key={tr.value}
              size="sm"
              variant={timeRange === tr.value ? "default" : "outline"}
              className="h-7 px-2.5 text-xs"
              onClick={() => { setTimeRange(tr.value); }}
            >
              {tr.label}
            </Button>
          ))}
        </div>
        {isLoading && (
          <span className="text-xs text-muted-foreground">Loading...</span>
        )}
      </div>

      {/* Historical charts */}
      <div className="grid gap-4 sm:grid-cols-2">
        <div className="h-64">
          <MetricChart title="CPU Usage" data={chartData} dataKey="cpuPercent" color="hsl(221, 83%, 53%)" timeRange={timeRange} />
        </div>
        <div className="h-64">
          <MetricChart title="Memory Usage" data={chartData} dataKey="memPercent" color="hsl(142, 71%, 45%)" timeRange={timeRange} />
        </div>
        <div className="h-64">
          <MetricChart title="Disk Read" data={chartData} dataKey="diskReadBps" color="hsl(38, 92%, 50%)" timeRange={timeRange} />
        </div>
        <div className="h-64">
          <MetricChart title="Disk Write" data={chartData} dataKey="diskWriteBps" color="hsl(0, 84%, 60%)" timeRange={timeRange} />
        </div>
        <div className="h-64">
          <MetricChart title="Network In" data={chartData} dataKey="netInBps" color="hsl(262, 83%, 58%)" timeRange={timeRange} />
        </div>
        <div className="h-64">
          <MetricChart title="Network Out" data={chartData} dataKey="netOutBps" color="hsl(330, 81%, 60%)" timeRange={timeRange} />
        </div>
      </div>
    </div>
  );
}
