import { useState, useRef, useEffect } from "react";
import { VMGuestToolsCard } from "@/features/guest-tools/components/VMGuestToolsCard";
import { VMGuestToolsSummary } from "@/features/guest-tools/components/VMGuestToolsSummary";
import { useParams, useSearchParams, Link } from "react-router-dom";
import {
  Monitor,
  Terminal,
  Pencil,
  Check,
  X,
  Container,
  ExternalLink,
} from "lucide-react";
import { OSIcon } from "@/components/OSIcon";
import { DetailField } from "@/components/DetailField";
import { DetailHeader, Fact } from "@/components/page/DetailHeader";
import {
  DefGrid,
  Panel,
  PropertyList,
  PropertyRow,
} from "@/components/page/Panel";
import { Stat, StatStrip } from "@/components/page/StatStrip";
import { classifyOS } from "@/lib/os-classify";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Skeleton } from "@/components/ui/skeleton";
import { MetricChart } from "@/features/dashboard/components/MetricChart";
import { TimeRangeSelector } from "@/features/dashboard/components/TimeRangeSelector";
import {
  useVMHistoricalMetrics,
  useVMPressure,
} from "@/features/dashboard/api/historical-queries";
import { PressureChart } from "@/features/dashboard/components/PressureChart";
import { useLiveSeries } from "@/features/dashboard/hooks/useLiveSeries";
import { latestValue, seriesTail } from "@/lib/metric-series";
import { useConsoleStore } from "@/stores/console-store";
import { openConsole } from "@/features/console/lib/open-console";
import type { ConsoleClickKeys } from "@/features/console/lib/console-session";
import {
  useCluster,
  useClusterNodes,
} from "@/features/clusters/api/cluster-queries";
import { useClusterMetrics } from "@/hooks/useMetrics";
import { useIsMobile } from "@/hooks/useIsMobile";
import { usePermissions } from "@/hooks/usePermissions";
import {
  useVM,
  useSetResourceConfig,
  useGuestAgentInfo,
  useResourcePools,
  useSetVMPool,
} from "../api/vm-queries";
import { VMActions } from "../components/VMActions";
import { GuestStateFact } from "../components/GuestStateFact";
import { VMConsolePreview } from "../components/VMConsolePreview";
import { CloneDialog } from "../components/CloneDialog";
import { CloneToTemplateDialog } from "../components/CloneToTemplateDialog";
import { DeployTemplateDialog } from "../components/DeployTemplateDialog";
import { MigrateJobDialog } from "../components/MigrateJobDialog";
import { DestroyDialog } from "../components/DestroyDialog";
import { ConvertToTemplateDialog } from "../components/ConvertToTemplateDialog";
import { CreateSnapshotDialog } from "../components/CreateSnapshotDialog";
import { SnapshotPanel } from "../components/SnapshotPanel";
import { CloudInitPanel } from "../components/CloudInitPanel";
import { HardwarePanel } from "../components/HardwarePanel";
import { ContainerResourcesPanel } from "../components/ContainerResourcesPanel";
import { SchedulePanel } from "../components/SchedulePanel";
import { BackupPanel } from "../components/BackupPanel";
import { VeeamProtectionCard } from "../components/VeeamProtectionCard";
import { GuestHistoryTab } from "../components/GuestHistoryTab";
import { LiveBlockJobsCard } from "../components/LiveBlockJobRows";
import { GuestRecentPanel } from "../components/GuestRecentPanel";
import type { ResourceKind } from "../types/vm";
import type { ResourceStatus } from "@/features/inventory/types/inventory";
import type { TimeRange } from "@/types/api";
import type { VmLiveMetric } from "@/types/ws";
import { formatBytes, formatBytesPerSecond, formatPercent } from "@/lib/format";
import { DATA_COLOR } from "@/lib/chart-colors";

export function VMDetailPage() {
  const {
    clusterId = "",
    vmId = "",
    kind: rawKind,
  } = useParams<{
    clusterId: string;
    vmId: string;
    kind: string;
  }>();
  const kind: ResourceKind =
    rawKind === "ct" || rawKind === "lxc" ? "ct" : "vm";
  // Read-only ?tab= deep link (e.g. the central Snapshots page links to
  // ?tab=snapshots). Only trusted on mount — AppShell remounts per pathname.
  const [searchParams] = useSearchParams();
  const requestedTab = searchParams.get("tab");
  const { canConsole, hasPermission } = usePermissions();
  // History is read from the audit log, and its rows open Proxmox task logs:
  // without both grants the tab would be a table of refusals.
  const canHistory =
    hasPermission("view", "audit") && hasPermission("view", "task");
  const validTabs = [
    ...(kind === "vm"
      ? [
          "overview",
          "hardware",
          "snapshots",
          "cloud-init",
          "backups",
          "schedules",
        ]
      : ["overview", "resources", "snapshots", "backups", "schedules"]),
    ...(canHistory ? ["history"] : []),
  ];
  const initialTab =
    requestedTab && validTabs.includes(requestedTab)
      ? requestedTab
      : "overview";
  const isMobile = useIsMobile();
  const { data: vm, isLoading, error } = useVM(clusterId, vmId, kind);

  const clusterMetrics = useClusterMetrics(clusterId);
  const liveMetric = clusterMetrics?.vmMetrics.get(vmId);

  const { data: nodes } = useClusterNodes(clusterId);
  const { data: cluster } = useCluster(clusterId);
  const [tab, setTab] = useState(initialTab);
  // The running task's banner gets the full width under the header; the
  // buttons that start the task sit in the header's corner.
  const [bannerSlot, setBannerSlot] = useState<HTMLDivElement | null>(null);
  // The rail's History link removes itself by switching tab; focus follows
  // to the tab it opened rather than falling back to the document.
  const historyTabRef = useRef<HTMLButtonElement>(null);
  const updateTabNode = useConsoleStore((s) => s.updateTabNode);

  // Resolve node name from node_id
  const nodeName = nodes?.find((n) => n.id === vm?.node_id)?.name ?? "";

  // When VM migrates to a different node, update any open console tabs
  // so they reconnect to the new node automatically.
  const prevNodeRef = useRef(nodeName);
  useEffect(() => {
    if (
      nodeName &&
      prevNodeRef.current &&
      nodeName !== prevNodeRef.current &&
      vm
    ) {
      updateTabNode(clusterId, vm.vmid, nodeName);
    }
    prevNodeRef.current = nodeName;
  }, [nodeName, clusterId, vm, updateTabNode]);

  const [snapshotOpen, setSnapshotOpen] = useState(false);
  const [cloneOpen, setCloneOpen] = useState(false);
  const [cloneToTemplateOpen, setCloneToTemplateOpen] = useState(false);
  const [deployOpen, setDeployOpen] = useState(false);
  const [migrateOpen, setMigrateOpen] = useState(false);
  const [destroyOpen, setDestroyOpen] = useState(false);
  const [convertTemplateOpen, setConvertTemplateOpen] = useState(false);
  const [renaming, setRenaming] = useState(false);
  const [newName, setNewName] = useState("");
  const renameInputRef = useRef<HTMLInputElement>(null);
  const setConfig = useSetResourceConfig();

  if (isLoading) {
    return (
      <div className="space-y-4">
        <Skeleton className="h-8 w-48" />
        <Skeleton className="h-48 w-full" />
      </div>
    );
  }

  if (error || !vm) {
    return (
      <div>
        <p className="text-destructive">
          {error?.message ?? "Resource not found"}
        </p>
        <Button variant="link" asChild className="mt-2 px-0">
          <Link to="/inventory">Back to Inventory</Link>
        </Button>
      </div>
    );
  }

  const normalizedStatus = vm.status.toLowerCase() as ResourceStatus;
  const consoleAllowed = canConsole(kind === "ct" ? "container" : "vm");
  // Guest tools are a Windows-only concept, so the tab only exists for them.
  const isWindowsGuest =
    classifyOS(vm.config_ostype) === "windows" ||
    classifyOS(vm.ostype) === "windows";

  function openGuestConsole(type: "terminal" | "vnc", keys: ConsoleClickKeys) {
    if (!vm) return;
    openConsole(
      {
        clusterID: clusterId,
        node: nodeName,
        vmid: vm.vmid,
        type:
          type === "vnc" ? "vm_vnc" : kind === "ct" ? "ct_vnc" : "vm_serial",
        resourceId: vmId,
        kind,
        guestName: vm.name,
      },
      keys,
    );
  }

  const previewAllowed =
    !isMobile &&
    kind === "vm" &&
    consoleAllowed &&
    normalizedStatus === "running" &&
    nodeName !== "";
  const hasKnownOS =
    classifyOS(vm.ostype) !== "unknown" ||
    classifyOS(vm.config_ostype) !== "unknown";

  return (
    <div className="space-y-5">
      <DetailHeader
        crumbs={[
          { label: "Inventory", to: "/inventory" },
          {
            label: cluster?.name ?? "Cluster",
            to: `/clusters/${clusterId}`,
          },
          ...(nodeName !== ""
            ? [
                {
                  label: nodeName,
                  to: `/clusters/${clusterId}/nodes/${vm.node_id}`,
                },
              ]
            : []),
          { label: vm.name },
        ]}
        icon={
          hasKnownOS ? (
            <OSIcon ostype={vm.ostype} configOstype={vm.config_ostype} />
          ) : kind === "ct" ? (
            <Container />
          ) : (
            <Monitor />
          )
        }
        title={
          renaming ? (
            <InlineRename
              inputRef={renameInputRef}
              value={newName}
              onChange={setNewName}
              isPending={setConfig.isPending}
              onConfirm={() => {
                const trimmed = newName.trim();
                if (trimmed.length === 0 || trimmed === vm.name) {
                  setRenaming(false);
                  return;
                }
                const field = kind === "ct" ? "hostname" : "name";
                setConfig.mutate(
                  {
                    clusterId,
                    resourceId: vmId,
                    kind,
                    fields: { [field]: trimmed },
                  },
                  {
                    onSuccess: () => {
                      setRenaming(false);
                    },
                  },
                );
              }}
              onCancel={() => {
                setRenaming(false);
              }}
            />
          ) : (
            vm.name
          )
        }
        titleAside={
          <>
            {!renaming && (
              <Button
                variant="ghost"
                size="sm"
                className="h-7 w-7 p-0 text-muted-foreground"
                onClick={() => {
                  setNewName(vm.name);
                  setRenaming(true);
                }}
                title="Rename"
              >
                <Pencil className="h-3.5 w-3.5" />
              </Button>
            )}
            {vm.template && <Badge variant="secondary">Template</Badge>}
          </>
        }
        facts={
          <>
            <GuestStateFact
              status={vm.status}
              runState={vm.run_state}
              uptime={vm.uptime}
            />
            <Fact label={kind === "ct" ? "CT" : "VM"}>{String(vm.vmid)}</Fact>
            {nodeName !== "" && (
              <Fact label="Node">
                <Link
                  to={`/clusters/${clusterId}/nodes/${vm.node_id}`}
                  className="hover:text-foreground hover:underline"
                >
                  {nodeName}
                </Link>
              </Fact>
            )}
            {vm.ha_state !== "" && <Fact label="HA">{vm.ha_state}</Fact>}
            <PoolFact clusterId={clusterId} vmId={vmId} currentPool={vm.pool} />
            {vm.tags.trim() !== "" && <Fact label="Tags">{vm.tags}</Fact>}
          </>
        }
        actions={
          /* Consoles open regardless of power state (iLO-style): a stopped
             guest parks as "powered off" with a Start button and connects
             automatically on power-on. Only templates can't be consoled.
             Hidden without the console:* permission — the mint would 403. */
          <VMActions
            clusterId={clusterId}
            resourceId={vmId}
            kind={kind}
            status={vm.status}
            runState={vm.run_state}
            name={vm.name}
            template={vm.template}
            bannerContainer={bannerSlot}
            onSnapshot={() => {
              setSnapshotOpen(true);
            }}
            onClone={() => {
              setCloneOpen(true);
            }}
            onCloneToTemplate={() => {
              setCloneToTemplateOpen(true);
            }}
            onDeploy={() => {
              setDeployOpen(true);
            }}
            onMigrate={() => {
              setMigrateOpen(true);
            }}
            onDestroy={() => {
              setDestroyOpen(true);
            }}
            onConvertToTemplate={() => {
              setConvertTemplateOpen(true);
            }}
          >
            {kind === "vm" && consoleAllowed && (
              <Button
                size="sm"
                className="gap-1.5"
                onClick={(e) => {
                  openGuestConsole("vnc", e);
                }}
                disabled={vm.template}
              >
                <Monitor className="h-4 w-4" />
                VNC Console
              </Button>
            )}
            {kind === "ct" && consoleAllowed && (
              <Button
                size="sm"
                className="gap-1.5"
                onClick={(e) => {
                  openGuestConsole("terminal", e);
                }}
                disabled={vm.template}
              >
                <Terminal className="h-4 w-4" />
                Attach
              </Button>
            )}
          </VMActions>
        }
      />
      <div ref={setBannerSlot} className="empty:hidden" />

      {/* Tabs */}
      <Tabs value={tab} onValueChange={setTab}>
        <TabsList>
          <TabsTrigger value="overview">Overview</TabsTrigger>
          {kind === "vm" && (
            <TabsTrigger value="hardware">Hardware</TabsTrigger>
          )}
          {kind === "ct" && (
            <TabsTrigger value="resources">Resources</TabsTrigger>
          )}
          <TabsTrigger value="snapshots">Snapshots</TabsTrigger>
          {kind === "vm" && (
            <TabsTrigger value="cloud-init">Cloud-Init</TabsTrigger>
          )}
          {kind === "vm" && isWindowsGuest && (
            <TabsTrigger value="guest-tools">Guest Tools</TabsTrigger>
          )}
          <TabsTrigger value="backups">Backups</TabsTrigger>
          <TabsTrigger value="schedules">Schedules</TabsTrigger>
          {canHistory && (
            <TabsTrigger ref={historyTabRef} value="history">
              History
            </TabsTrigger>
          )}
        </TabsList>

        <TabsContent value="overview" className="mt-5">
          {/* Two columns that fold into one: what the guest is doing on the
              left, what it is and what happened to it on the right. */}
          <div className="flex flex-wrap items-start gap-4">
            <div className="flex min-w-0 flex-[999_1_560px] flex-col gap-4">
              <VMMetricsPanel
                clusterId={clusterId}
                vmId={vmId}
                liveMetric={liveMetric}
                memTotalBytes={vm.mem_total}
                cpuCount={vm.cpu_count}
              />
              {/* Guest Agent — QEMU VMs only, when running */}
              {kind === "vm" && normalizedStatus === "running" && (
                <GuestAgentSection
                  clusterId={clusterId}
                  vmId={vmId}
                  vmid={vm.vmid}
                  configOstype={vm.config_ostype}
                  ostype={vm.ostype}
                />
              )}
              {/* Without the History tab, where the live block jobs sit,
                  they are shown here instead. */}
              {!canHistory &&
                kind === "vm" &&
                normalizedStatus === "running" && (
                  <LiveBlockJobsCard clusterId={clusterId} vmId={vmId} />
                )}
            </div>
            <div className="flex min-w-0 flex-[1_1_300px] flex-col gap-4">
              {/* Desktop-only: a phone has the Console button, and a live
                  framebuffer is too much to stream into a thumbnail there. */}
              {!isMobile && consoleAllowed && !vm.template && (
                <ConsolePanel
                  idleText={
                    kind === "ct"
                      ? "Attach to open a terminal"
                      : normalizedStatus === "running"
                        ? "Console unavailable"
                        : "Powered off"
                  }
                  onOpenInTab={() => {
                    openGuestConsole(kind === "ct" ? "terminal" : "vnc", {
                      ctrlKey: true,
                      metaKey: false,
                    });
                  }}
                >
                  {previewAllowed && (
                    <VMConsolePreview
                      clusterId={clusterId}
                      node={nodeName}
                      vmid={vm.vmid}
                      className="aspect-[16/10] h-auto w-full rounded-[7px] border-0"
                      onOpen={(e) => {
                        openGuestConsole("vnc", e);
                      }}
                    />
                  )}
                </ConsolePanel>
              )}
              <Panel title="Configuration">
                <PropertyList>
                  <PropertyRow label="Type">
                    {vm.type === "lxc" ? "LXC" : "QEMU"}
                  </PropertyRow>
                  <PropertyRow label="CPUs">{String(vm.cpu_count)}</PropertyRow>
                  <PropertyRow label="Memory">
                    {formatBytes(vm.mem_total)}
                  </PropertyRow>
                  <PropertyRow label="Disk">
                    {formatBytes(vm.disk_total)}
                  </PropertyRow>
                </PropertyList>
              </Panel>
              {canHistory && (
                <GuestRecentPanel
                  clusterId={clusterId}
                  vmid={vm.vmid}
                  onOpenHistory={() => {
                    setTab("history");
                    historyTabRef.current?.focus();
                  }}
                />
              )}
            </div>
          </div>
        </TabsContent>

        {kind === "vm" && (
          <TabsContent value="hardware" className="mt-4">
            <HardwarePanel
              clusterId={clusterId}
              vmId={vmId}
              vmStatus={vm.status}
              nodeName={nodeName}
            />
          </TabsContent>
        )}

        {kind === "ct" && (
          <TabsContent value="resources" className="mt-4">
            <ContainerResourcesPanel
              clusterId={clusterId}
              ctId={vmId}
              ctStatus={vm.status}
              nodeName={nodeName}
            />
          </TabsContent>
        )}

        <TabsContent value="snapshots" className="mt-4">
          <SnapshotPanel
            clusterId={clusterId}
            resourceId={vmId}
            kind={kind}
            resourceName={vm.name}
          />
        </TabsContent>

        {kind === "vm" && (
          <TabsContent value="cloud-init" className="mt-4">
            <CloudInitPanel clusterId={clusterId} vmId={vmId} />
          </TabsContent>
        )}

        {kind === "vm" && isWindowsGuest && (
          <TabsContent value="guest-tools" className="mt-4">
            <VMGuestToolsCard clusterId={clusterId} vmid={vm.vmid} />
          </TabsContent>
        )}

        <TabsContent value="backups" className="mt-4 space-y-4">
          {/*
            Both providers on one tab, on purpose. A guest can be protected by
            PBS, by Veeam, by both, or by neither, and an operator asking "is
            this backed up" has to see the whole answer in one place — a
            separate Veeam tab would let them read half of it and stop.
          */}
          <BackupPanel
            vmid={vm.vmid}
            clusterId={clusterId}
            nodeName={nodeName}
            kind={kind}
          />
          <VeeamProtectionCard clusterId={clusterId} vmId={vmId} />
        </TabsContent>

        <TabsContent value="schedules" className="mt-4">
          <SchedulePanel
            clusterId={clusterId}
            kind={kind}
            vmid={vm.vmid}
            node={nodeName}
          />
        </TabsContent>

        {canHistory && (
          <TabsContent value="history" className="mt-4">
            <GuestHistoryTab
              clusterId={clusterId}
              vmId={vmId}
              vmid={vm.vmid}
              showBlockJobs={kind === "vm" && normalizedStatus === "running"}
            />
          </TabsContent>
        )}
      </Tabs>

      {/* Dialogs */}
      <CreateSnapshotDialog
        open={snapshotOpen}
        onOpenChange={setSnapshotOpen}
        clusterId={clusterId}
        resourceId={vmId}
        kind={kind}
        resourceName={vm.name}
      />
      <CloneDialog
        open={cloneOpen}
        onOpenChange={setCloneOpen}
        clusterId={clusterId}
        resourceId={vmId}
        kind={kind}
        sourceName={vm.name}
      />
      <MigrateJobDialog
        open={migrateOpen}
        onOpenChange={setMigrateOpen}
        clusterId={clusterId}
        resourceId={vmId}
        vmid={vm.vmid}
        vmName={vm.name}
        kind={kind}
        currentNode={nodeName}
        status={vm.status}
      />
      <DestroyDialog
        open={destroyOpen}
        onOpenChange={setDestroyOpen}
        clusterId={clusterId}
        resourceId={vmId}
        kind={kind}
        resourceName={vm.name}
      />

      <CloneToTemplateDialog
        open={cloneToTemplateOpen}
        onOpenChange={setCloneToTemplateOpen}
        clusterId={clusterId}
        resourceId={vmId}
        kind={kind}
        sourceName={vm.name}
      />

      <DeployTemplateDialog
        open={deployOpen}
        onOpenChange={setDeployOpen}
        clusterId={clusterId}
        vmId={vmId}
        kind={kind}
        templateName={vm.name}
      />

      <ConvertToTemplateDialog
        open={convertTemplateOpen}
        onOpenChange={setConvertTemplateOpen}
        clusterId={clusterId}
        resourceId={vmId}
        kind={kind}
        resourceName={vm.name}
      />
    </div>
  );
}

/** The rail's console block: the live display when there is one, and a way in. */
function ConsolePanel({
  children,
  idleText,
  onOpenInTab,
}: {
  /** The live preview. Absent (or false) shows the idle tile instead. */
  children?: React.ReactNode;
  idleText: string;
  onOpenInTab: () => void;
}) {
  return (
    <Panel flush bodyClassName="p-2.5 pb-2">
      {children !== undefined && children !== false ? (
        children
      ) : (
        <div className="grid aspect-[16/10] place-items-center rounded-[7px] bg-black text-xs text-neutral-400">
          {idleText}
        </div>
      )}
      <div className="flex items-center gap-2 pl-2 pt-2">
        <h2 className="flex-1 text-[13.5px] font-semibold">Console</h2>
        <Button
          variant="ghost"
          size="sm"
          className="h-7 gap-1.5 px-2 text-primary hover:text-primary"
          onClick={onOpenInTab}
        >
          Open in a tab
          <ExternalLink className="h-3.5 w-3.5" />
        </Button>
      </div>
    </Panel>
  );
}

/** How many interfaces the Overview lists before it says how many more. */
const AGENT_INTERFACE_LIMIT = 3;

function GuestAgentSection({
  clusterId,
  vmId,
  vmid,
  configOstype,
  ostype,
}: {
  clusterId: string;
  vmId: string;
  vmid: number;
  configOstype: string;
  ostype: string;
}) {
  const { data, isLoading } = useGuestAgentInfo(clusterId, vmId, true);
  const interfaces = (data?.network_interfaces ?? []).filter(
    (iface) => iface.name !== "lo",
  );
  const shown = interfaces.slice(0, AGENT_INTERFACE_LIMIT);

  return (
    <Panel
      title="Guest agent"
      actions={
        <span className="flex items-center gap-[7px] text-sm text-muted-foreground">
          {isLoading ? (
            "Checking..."
          ) : data?.running ? (
            <>
              <span
                aria-hidden="true"
                className="h-[7px] w-[7px] rounded-full bg-emerald-500"
              />
              Connected
            </>
          ) : (
            "Not running"
          )}
        </span>
      }
    >
      {isLoading || !data?.running ? (
        <p className="text-sm text-muted-foreground">
          {isLoading
            ? "Asking the guest agent..."
            : "The QEMU guest agent is not answering, so there is nothing to read from inside the guest."}
        </p>
      ) : (
        <div className="space-y-5">
          <DefGrid>
            {data.os_info?.["pretty-name"] && (
              <DetailField label="Operating system">
                {data.os_info["pretty-name"]}
              </DetailField>
            )}
            {data.os_info?.["kernel-release"] && (
              <DetailField label="Kernel">
                {data.os_info["kernel-release"]}
              </DetailField>
            )}
            {data.os_info?.machine && (
              <DetailField label="Architecture">
                {data.os_info.machine}
              </DetailField>
            )}
            {data.os_info?.name && (
              <DetailField label="Hostname">{data.os_info.name}</DetailField>
            )}
            {/* Read-only status so "is an update waiting?" is answerable from
                Overview. The controls live on the Guest Tools tab. */}
            <VMGuestToolsSummary
              clusterId={clusterId}
              vmid={vmid}
              configOstype={configOstype}
              ostype={ostype}
            />
          </DefGrid>
          {shown.length > 0 && (
            <div className="overflow-x-auto">
              <table className="w-full min-w-[26rem] text-left text-sm">
                <caption className="pb-1.5 text-left font-semibold">
                  Network interfaces
                </caption>
                <thead>
                  <tr className="border-b">
                    <th scope="col" className="py-1.5 pr-4 font-normal">
                      Interface
                    </th>
                    <th scope="col" className="py-1.5 pr-4 font-normal">
                      MAC address
                    </th>
                    <th scope="col" className="py-1.5 font-normal">
                      Addresses
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {shown.map((iface) => (
                    <tr key={iface.name} className="border-b last:border-0">
                      <td className="py-2 pr-4 align-top">{iface.name}</td>
                      <td className="py-2 pr-4 align-top font-mono text-[12.5px] text-muted-foreground">
                        {iface["hardware-address"] !== ""
                          ? iface["hardware-address"]
                          : "—"}
                      </td>
                      <td className="py-2 align-top">
                        <div className="flex flex-wrap gap-1.5 font-mono text-[12.5px]">
                          {(iface["ip-addresses"] ?? []).map((ip) => (
                            <span
                              key={`${ip["ip-address"]}/${String(ip.prefix)}`}
                              className="rounded-[5px] bg-foreground/[0.07] px-1.5 py-0.5"
                            >
                              {ip["ip-address"]}/{String(ip.prefix)}
                            </span>
                          ))}
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
              {interfaces.length > shown.length && (
                <p className="pt-2 text-[12.5px] text-muted-foreground">
                  and {String(interfaces.length - shown.length)} more
                </p>
              )}
            </div>
          )}
        </div>
      )}
    </Panel>
  );
}

/** Which measure the large chart under the strip is drawing. */
type MetricFocus = "cpu" | "memory" | "disk" | "network";

function VMMetricsPanel({
  clusterId,
  vmId,
  liveMetric,
  memTotalBytes,
  cpuCount,
}: {
  clusterId: string;
  vmId: string;
  liveMetric: VmLiveMetric | undefined;
  /** VM configured memory size; turns the memory percent into exact used/total bytes. */
  memTotalBytes?: number | undefined;
  cpuCount: number;
}) {
  const [timeRange, setTimeRange] = useState<TimeRange>("live");
  const [focus, setFocus] = useState<MetricFocus>("cpu");
  const isLive = timeRange === "live";
  // Live charts are seeded with the last hour of stored data so they're full
  // on page load; WS ticks are appended on top.
  const { data: historicalData, isLoading } = useVMHistoricalMetrics(
    clusterId,
    vmId,
    isLive ? "1h" : timeRange,
    isLive,
  );

  const chartData = useLiveSeries(historicalData, liveMetric, isLive);
  const pressure = useVMPressure(clusterId, vmId, timeRange);

  const cpu = latestValue(chartData, "cpuPercent");
  const mem = latestValue(chartData, "memPercent");
  const diskRead = latestValue(chartData, "diskReadBps");
  const diskWrite = latestValue(chartData, "diskWriteBps");
  const netIn = latestValue(chartData, "netInBps");
  const netOut = latestValue(chartData, "netOutBps");
  const memDetail =
    mem !== undefined && memTotalBytes !== undefined && memTotalBytes > 0
      ? `${formatBytes((mem / 100) * memTotalBytes)} of ${formatBytes(memTotalBytes)}`
      : undefined;
  const rate = (a: number | undefined, b: number | undefined) =>
    a === undefined && b === undefined
      ? "—"
      : formatBytesPerSecond((a ?? 0) + (b ?? 0));
  const pair = (
    a: number | undefined,
    b: number | undefined,
    aLabel: string,
    bLabel: string,
  ) =>
    a === undefined && b === undefined
      ? undefined
      : `${formatBytesPerSecond(a ?? 0)} ${aLabel}, ${formatBytesPerSecond(b ?? 0)} ${bLabel}`;

  const chart = (
    title: string,
    dataKey: React.ComponentProps<typeof MetricChart>["dataKey"],
    headerDetail?: string,
  ) => (
    <div className="h-64 min-w-0">
      <MetricChart
        bare
        title={title}
        data={chartData}
        dataKey={dataKey}
        color={DATA_COLOR}
        timeRange={timeRange}
        headerDetail={headerDetail}
      />
    </div>
  );

  return (
    <>
      <Panel
        flush
        title="Performance"
        aside={isLoading ? "Loading..." : undefined}
        actions={
          <TimeRangeSelector value={timeRange} onChange={setTimeRange} />
        }
      >
        {/* The strip is the summary and the chart's selector in one: each
            measure's latest reading over its recent shape, and the one that
            is pressed drawn large below. */}
        <StatStrip>
          <Stat
            label="CPU"
            value={cpu === undefined ? "—" : formatPercent(cpu)}
            sub={`of ${String(cpuCount)} vCPU`}
            trend={seriesTail(chartData, (p) => p.cpuPercent)}
            trendMax={100}
            selected={focus === "cpu"}
            onSelect={() => {
              setFocus("cpu");
            }}
          />
          <Stat
            label="Memory"
            value={mem === undefined ? "—" : formatPercent(mem)}
            sub={memDetail}
            trend={seriesTail(chartData, (p) => p.memPercent)}
            trendMax={100}
            selected={focus === "memory"}
            onSelect={() => {
              setFocus("memory");
            }}
          />
          <Stat
            label="Disk I/O"
            value={rate(diskRead, diskWrite)}
            sub={pair(diskRead, diskWrite, "read", "write")}
            trend={seriesTail(chartData, (p) =>
              p.diskReadBps === null && p.diskWriteBps === null
                ? null
                : (p.diskReadBps ?? 0) + (p.diskWriteBps ?? 0),
            )}
            selected={focus === "disk"}
            onSelect={() => {
              setFocus("disk");
            }}
          />
          <Stat
            label="Network"
            value={rate(netIn, netOut)}
            sub={pair(netIn, netOut, "in", "out")}
            trend={seriesTail(chartData, (p) =>
              p.netInBps === null && p.netOutBps === null
                ? null
                : (p.netInBps ?? 0) + (p.netOutBps ?? 0),
            )}
            selected={focus === "network"}
            onSelect={() => {
              setFocus("network");
            }}
          />
        </StatStrip>
        <div className="-mt-px border-t px-[18px] pt-4">
          {focus === "cpu" && chart("CPU Usage", "cpuPercent")}
          {focus === "memory" && chart("Memory Usage", "memPercent", memDetail)}
          {focus === "disk" && (
            <div className="grid gap-6 sm:grid-cols-2">
              {chart("Disk Read", "diskReadBps")}
              {chart("Disk Write", "diskWriteBps")}
            </div>
          )}
          {focus === "network" && (
            <div className="grid gap-6 sm:grid-cols-2">
              {chart("Network In", "netInBps")}
              {chart("Network Out", "netOutBps")}
            </div>
          )}
        </div>
      </Panel>
      <PressureChart
        data={pressure.data}
        timeRange={timeRange}
        isLoading={pressure.isLoading}
        isError={pressure.isError}
      />
    </>
  );
}

/** The guest's pool on the facts line, changed in place. */
function PoolFact({
  clusterId,
  vmId,
  currentPool,
}: {
  clusterId: string;
  vmId: string;
  currentPool: string;
}) {
  const { data: pools } = useResourcePools(clusterId);
  const setPool = useSetVMPool(clusterId, vmId);
  const [editing, setEditing] = useState(false);
  const [selected, setSelected] = useState(currentPool);

  // Sync local state when prop changes (e.g. after mutation invalidates query).
  useEffect(() => {
    setSelected(currentPool);
  }, [currentPool]);

  if (!editing) {
    return (
      <Fact label="Pool" className="items-center">
        <span className="flex min-w-0 items-center gap-1">
          <span
            className="min-w-0 max-w-[16rem] truncate"
            title={currentPool || undefined}
          >
            {currentPool || "none"}
          </span>
          <Button
            variant="ghost"
            size="sm"
            className="h-5 w-5 shrink-0 p-0"
            onClick={() => {
              setEditing(true);
            }}
            title="Change pool"
          >
            <Pencil className="h-3 w-3" />
          </Button>
        </span>
      </Fact>
    );
  }

  return (
    <Fact label="Pool" className="items-center">
      <span className="flex min-w-0 items-center gap-1">
        <select
          className="h-7 min-w-0 max-w-[16rem] rounded-md border bg-transparent px-1.5 text-sm text-foreground outline-hidden focus:ring-2 focus:ring-ring"
          value={selected}
          onChange={(e) => {
            setSelected(e.target.value);
          }}
          disabled={setPool.isPending}
          title={selected || "None"}
        >
          <option value="">None</option>
          {pools?.map((p) => (
            <option key={p.poolid} value={p.poolid}>
              {p.poolid}
            </option>
          ))}
        </select>
        <Button
          variant="ghost"
          size="sm"
          className="h-5 w-5 shrink-0 p-0 text-emerald-600"
          disabled={setPool.isPending}
          title="Save pool"
          onClick={() => {
            setPool.mutate(selected, {
              onSuccess: () => {
                setEditing(false);
              },
            });
          }}
        >
          <Check className="h-3 w-3" />
        </Button>
        <Button
          variant="ghost"
          size="sm"
          className="h-5 w-5 shrink-0 p-0 text-destructive"
          disabled={setPool.isPending}
          title="Cancel"
          onClick={() => {
            setSelected(currentPool);
            setEditing(false);
          }}
        >
          <X className="h-3 w-3" />
        </Button>
      </span>
    </Fact>
  );
}

function InlineRename({
  inputRef,
  value,
  onChange,
  isPending,
  onConfirm,
  onCancel,
}: {
  inputRef: React.RefObject<HTMLInputElement | null>;
  value: string;
  onChange: (v: string) => void;
  isPending: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}) {
  useEffect(() => {
    inputRef.current?.focus();
    inputRef.current?.select();
  }, [inputRef]);

  return (
    <div className="flex items-center gap-1">
      <input
        ref={inputRef}
        aria-label="Name"
        className="h-9 min-w-0 rounded-md border bg-transparent px-2 text-xl font-[650] outline-hidden [font-stretch:112%] focus:ring-2 focus:ring-ring"
        value={value}
        onChange={(e) => {
          onChange(e.target.value);
        }}
        onKeyDown={(e) => {
          if (e.key === "Enter") onConfirm();
          if (e.key === "Escape") onCancel();
        }}
        disabled={isPending}
      />
      <Button
        aria-label="Confirm rename"
        variant="ghost"
        size="sm"
        className="h-7 w-7 p-0 text-emerald-600"
        onClick={onConfirm}
        disabled={isPending}
      >
        <Check className="h-4 w-4" />
      </Button>
      <Button
        aria-label="Cancel rename"
        variant="ghost"
        size="sm"
        className="h-7 w-7 p-0 text-destructive"
        onClick={onCancel}
        disabled={isPending}
      >
        <X className="h-4 w-4" />
      </Button>
    </div>
  );
}
