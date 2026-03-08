import { useEffect, useMemo, useState } from "react";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Checkbox } from "@/components/ui/checkbox";
import {
  useClusterNodes,
  useClusterStorage,
  useClusterVMs,
} from "@/features/clusters/api/cluster-queries";
import type { NodeResponse } from "@/types/api";
import { useStorageContent } from "@/features/storage/api/storage-queries";
import {
  useClusterVMIDs,
  useCreateContainer,
  useResourcePools,
} from "../api/vm-queries";
import { TaskProgressBanner } from "./TaskProgressBanner";

interface CreateCTDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  clusterId: string;
}

const selectClass =
  "flex h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-sm transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring";

type Step =
  | "general"
  | "template"
  | "rootdisk"
  | "cpu_memory"
  | "network"
  | "dns_options"
  | "confirm";

const steps: Step[] = [
  "general",
  "template",
  "rootdisk",
  "cpu_memory",
  "network",
  "dns_options",
  "confirm",
];

const stepLabels: Record<Step, string> = {
  general: "General",
  template: "Template",
  rootdisk: "Root Disk",
  cpu_memory: "CPU & Memory",
  network: "Network",
  dns_options: "DNS & Options",
  confirm: "Confirm",
};

type IPv4Mode = "dhcp" | "static" | "none";
type IPv6Mode = "dhcp" | "slaac" | "static" | "none";

export function CreateCTDialog({
  open,
  onOpenChange,
  clusterId,
}: CreateCTDialogProps) {
  const { data: nodes } = useClusterNodes(clusterId);
  const { data: storageList } = useClusterStorage(clusterId);
  const { data: usedVMIDs } = useClusterVMIDs(clusterId);
  const { data: pools } = useResourcePools(clusterId);
  const { data: clusterVMs } = useClusterVMs(clusterId);
  const createMutation = useCreateContainer();

  // Template storage selection for browsing vztmpl content
  const [templateStorageId, setTemplateStorageId] = useState("");
  const { data: templateContent } = useStorageContent(
    clusterId,
    templateStorageId,
  );

  const templateStoragePools = useMemo(() => {
    if (!storageList) return [];
    const seen = new Set<string>();
    return storageList
      .filter((s) => {
        if (!s.active || !s.enabled || !s.content.includes("vztmpl"))
          return false;
        if (seen.has(s.storage)) return false;
        seen.add(s.storage);
        return true;
      })
      .sort((a, b) => a.storage.localeCompare(b.storage));
  }, [storageList]);

  const templates = useMemo(() => {
    if (!templateContent) return [];
    return templateContent
      .filter((item) => item.content === "vztmpl")
      .sort((a, b) => b.ctime - a.ctime);
  }, [templateContent]);

  const rootdirStoragePools = useMemo(() => {
    if (!storageList) return [];
    const seen = new Set<string>();
    return storageList
      .filter((s) => {
        if (!s.active || !s.enabled || !s.content.includes("rootdir"))
          return false;
        if (seen.has(s.storage)) return false;
        seen.add(s.storage);
        return true;
      })
      .sort((a, b) => a.storage.localeCompare(b.storage));
  }, [storageList]);

  // Best available node: sort by available resources (total - allocated)
  const bestNode = useMemo(() => {
    if (!nodes || nodes.length === 0) return "";
    if (!clusterVMs) return "";

    const allocated = new Map<string, { cpu: number; mem: number }>();
    for (const vm of clusterVMs) {
      const nodeEntry = nodes.find((n: NodeResponse) => n.id === vm.node_id);
      if (!nodeEntry) continue;
      const existing = allocated.get(nodeEntry.name) ?? { cpu: 0, mem: 0 };
      existing.cpu += vm.cpu_count;
      existing.mem += vm.mem_total;
      allocated.set(nodeEntry.name, existing);
    }

    let best = nodes[0];
    let bestScore = -1;
    for (const n of nodes) {
      if (n.status !== "online") continue;
      const alloc = allocated.get(n.name) ?? { cpu: 0, mem: 0 };
      const cpuFree = n.cpu_count > 0 ? (n.cpu_count - alloc.cpu) / n.cpu_count : 0;
      const memFree = n.mem_total > 0 ? (n.mem_total - alloc.mem) / n.mem_total : 0;
      const score = cpuFree * 0.5 + memFree * 0.5;
      if (score > bestScore) {
        bestScore = score;
        best = n;
      }
    }
    return best?.name ?? "";
  }, [nodes, clusterVMs]);

  const nextAvailableId = useMemo(() => {
    if (!usedVMIDs || usedVMIDs.size === 0) return 100;
    let candidate = 100;
    while (usedVMIDs.has(candidate)) candidate++;
    return candidate;
  }, [usedVMIDs]);

  const [step, setStep] = useState<Step>("general");
  const [upid, setUpid] = useState<string | null>(null);

  // Step 1: General
  const [vmid, setVmid] = useState("");
  const [hostname, setHostname] = useState("");
  const [node, setNode] = useState("");
  const [pool, setPool] = useState("");
  const [unprivileged, setUnprivileged] = useState(true);
  const [nesting, setNesting] = useState(false);

  // Step 2: Template
  const [selectedTemplate, setSelectedTemplate] = useState("");

  // Step 3: Root Disk
  const [rootStorageId, setRootStorageId] = useState(""); // UUID for display
  const [rootStorageName, setRootStorageName] = useState(""); // name for Proxmox API
  const [diskSize, setDiskSize] = useState("8");

  // Step 4: CPU & Memory
  const [cores, setCores] = useState("1");
  const [cpuLimit, setCpuLimit] = useState("");
  const [cpuUnits, setCpuUnits] = useState("1024");
  const [arch, setArch] = useState("amd64");
  const [memory, setMemory] = useState("512");
  const [swap, setSwap] = useState("512");

  // Step 5: Network
  const [nicName, setNicName] = useState("eth0");
  const [bridge, setBridge] = useState("vmbr0");
  const [macAddress, setMacAddress] = useState("");
  const [vlanTag, setVlanTag] = useState("");
  const [rateLimit, setRateLimit] = useState("");
  const [firewall, setFirewall] = useState(false);
  const [mtu, setMtu] = useState("");
  const [ipv4Mode, setIpv4Mode] = useState<IPv4Mode>("dhcp");
  const [ipv4Addr, setIpv4Addr] = useState("");
  const [ipv4Gw, setIpv4Gw] = useState("");
  const [ipv6Mode, setIpv6Mode] = useState<IPv6Mode>("none");
  const [ipv6Addr, setIpv6Addr] = useState("");
  const [ipv6Gw, setIpv6Gw] = useState("");

  // Step 6: DNS & Options
  const [searchdomain, setSearchdomain] = useState("");
  const [nameserver, setNameserver] = useState("");
  const [description, setDescription] = useState("");
  const [tags, setTags] = useState("");
  const [onboot, setOnboot] = useState(false);
  const [protection, setProtection] = useState(false);
  const [startup, setStartup] = useState("");
  const [cmode, setCmode] = useState("tty");
  const [startAfter, setStartAfter] = useState(false);
  const [password, setPassword] = useState("");
  const [passwordConfirm, setPasswordConfirm] = useState("");
  const [sshKeys, setSshKeys] = useState("");

  useEffect(() => {
    if (open && vmid === "" && usedVMIDs) {
      setVmid(String(nextAvailableId));
    }
    if (open && node === "" && bestNode) {
      setNode(bestNode);
    }
  }, [open, usedVMIDs, nextAvailableId, vmid, bestNode, node]);

  const isDuplicate = usedVMIDs ? usedVMIDs.has(Number(vmid)) : false;
  const stepIdx = steps.indexOf(step);

  function extractFilename(volid: string): string {
    // volid like "local:vztmpl/ubuntu-22.04-standard_22.04-1_amd64.tar.zst"
    const parts = volid.split("/");
    return parts.length > 1 ? (parts[parts.length - 1] ?? volid) : volid;
  }

  function buildNet0(): string {
    let net = `name=${nicName},bridge=${bridge}`;
    if (macAddress) net += `,hwaddr=${macAddress}`;
    if (vlanTag) net += `,tag=${vlanTag}`;
    if (rateLimit) net += `,rate=${rateLimit}`;
    if (firewall) net += `,firewall=1`;
    if (mtu) net += `,mtu=${mtu}`;
    if (ipv4Mode === "dhcp") net += `,ip=dhcp`;
    else if (ipv4Mode === "static" && ipv4Addr) {
      net += `,ip=${ipv4Addr}`;
      if (ipv4Gw) net += `,gw=${ipv4Gw}`;
    }
    if (ipv6Mode === "dhcp") net += `,ip6=dhcp`;
    else if (ipv6Mode === "slaac") net += `,ip6=auto`;
    else if (ipv6Mode === "static" && ipv6Addr) {
      net += `,ip6=${ipv6Addr}`;
      if (ipv6Gw) net += `,gw6=${ipv6Gw}`;
    }
    return net;
  }

  function handleSubmit() {
    const rootfs =
      rootStorageName && diskSize ? `${rootStorageName}:${diskSize}` : "";

    const extra: Record<string, string> = {};

    // Features
    const featureParts: string[] = [];
    if (nesting) featureParts.push("nesting=1");
    if (featureParts.length > 0) extra["features"] = featureParts.join(",");

    // CPU
    if (cpuLimit) extra["cpulimit"] = cpuLimit;
    if (cpuUnits && cpuUnits !== "1024") extra["cpuunits"] = cpuUnits;
    if (arch && arch !== "amd64") extra["arch"] = arch;

    // Options
    if (onboot) extra["onboot"] = "1";
    if (protection) extra["protection"] = "1";
    if (startup) extra["startup"] = startup;
    if (cmode && cmode !== "tty") extra["cmode"] = cmode;

    createMutation.mutate(
      {
        clusterId,
        body: {
          vmid: Number(vmid),
          hostname,
          node,
          ostemplate: selectedTemplate,
          storage: rootStorageName,
          rootfs,
          memory: Number(memory),
          swap: Number(swap),
          cores: Number(cores),
          net0: buildNet0(),
          password,
          ssh_keys: sshKeys,
          unprivileged,
          start: startAfter,
          description: description || undefined,
          tags: tags || undefined,
          pool: pool || undefined,
          nameserver: nameserver || undefined,
          searchdomain: searchdomain || undefined,
          extra: Object.keys(extra).length > 0 ? extra : undefined,
        },
      },
      {
        onSuccess: (data) => {
          setUpid(data.upid);
        },
      },
    );
  }

  function handleClose() {
    setStep("general");
    setUpid(null);
    setVmid("");
    setHostname("");
    setNode("");
    setPool("");
    setUnprivileged(true);
    setNesting(false);
    setTemplateStorageId("");
    setSelectedTemplate("");
    setRootStorageId("");
    setRootStorageName("");
    setDiskSize("8");
    setCores("1");
    setCpuLimit("");
    setCpuUnits("1024");
    setArch("amd64");
    setMemory("512");
    setSwap("512");
    setNicName("eth0");
    setBridge("vmbr0");
    setMacAddress("");
    setVlanTag("");
    setRateLimit("");
    setFirewall(false);
    setMtu("");
    setIpv4Mode("dhcp");
    setIpv4Addr("");
    setIpv4Gw("");
    setIpv6Mode("none");
    setIpv6Addr("");
    setIpv6Gw("");
    setSearchdomain("");
    setNameserver("");
    setDescription("");
    setTags("");
    setOnboot(false);
    setProtection(false);
    setStartup("");
    setCmode("tty");
    setStartAfter(false);
    setPassword("");
    setPasswordConfirm("");
    setSshKeys("");
    createMutation.reset();
    onOpenChange(false);
  }

  const passwordMismatch =
    password !== "" && passwordConfirm !== "" && password !== passwordConfirm;

  const canProceed = (() => {
    switch (step) {
      case "general":
        return Number(vmid) > 0 && node !== "";
      case "template":
        return selectedTemplate !== "";
      case "rootdisk":
        return Number(diskSize) > 0;
      case "cpu_memory":
        return Number(cores) > 0 && Number(memory) > 0;
      case "network":
        return nicName !== "" && bridge !== "";
      case "dns_options":
        return !passwordMismatch;
      default:
        return true;
    }
  })();

  function formatSize(bytes: number): string {
    if (bytes >= 1073741824) return `${(bytes / 1073741824).toFixed(1)} GB`;
    if (bytes >= 1048576) return `${(bytes / 1048576).toFixed(1)} MB`;
    return `${String(bytes)} B`;
  }

  return (
    <Dialog open={open} onOpenChange={handleClose}>
      <DialogContent className="max-w-2xl">
        <DialogHeader>
          <DialogTitle>Create Container</DialogTitle>
          <DialogDescription>
            Step {stepIdx + 1} of {steps.length}: {stepLabels[step]}
          </DialogDescription>
        </DialogHeader>

        {upid ? (
          <TaskProgressBanner
            clusterId={clusterId}
            upid={upid}
            onComplete={() => {
              handleClose();
            }}
            description={`Create CT ${hostname || vmid}`}
          />
        ) : (
          <div className="space-y-4">
            {/* Step 1: General */}
            {step === "general" && (
              <div className="grid gap-4 sm:grid-cols-2">
                <div className="space-y-2">
                  <Label>VMID</Label>
                  <Input
                    type="number"
                    min={1}
                    value={vmid}
                    onChange={(e) => {
                      setVmid(e.target.value);
                    }}
                  />
                  {isDuplicate && (
                    <p className="text-xs text-yellow-600 dark:text-yellow-500">
                      VMID may already be in use
                    </p>
                  )}
                </div>
                <div className="space-y-2">
                  <Label>Hostname</Label>
                  <Input
                    value={hostname}
                    onChange={(e) => {
                      setHostname(e.target.value);
                    }}
                    placeholder="my-container"
                  />
                </div>
                <div className="space-y-2">
                  <Label>Target Node</Label>
                  <select
                    value={node}
                    onChange={(e) => {
                      setNode(e.target.value);
                    }}
                    className={selectClass}
                  >
                    <option value="">Select node</option>
                    {nodes?.map((n) => (
                      <option key={n.id} value={n.name}>
                        {n.name}
                      </option>
                    ))}
                  </select>
                </div>
                <div className="space-y-2">
                  <Label>Resource Pool</Label>
                  <select
                    value={pool}
                    onChange={(e) => {
                      setPool(e.target.value);
                    }}
                    className={selectClass}
                  >
                    <option value="">None</option>
                    {pools?.map((p) => (
                      <option key={p.poolid} value={p.poolid}>
                        {p.poolid}
                        {p.comment ? ` — ${p.comment}` : ""}
                      </option>
                    ))}
                  </select>
                </div>
                <div className="flex items-center gap-2">
                  <Checkbox
                    id="ct-unprivileged"
                    checked={unprivileged}
                    onCheckedChange={(checked) => {
                      setUnprivileged(Boolean(checked));
                    }}
                  />
                  <Label htmlFor="ct-unprivileged" className="text-sm">
                    Unprivileged container
                  </Label>
                </div>
                <div className="flex items-center gap-2">
                  <Checkbox
                    id="ct-nesting"
                    checked={nesting}
                    onCheckedChange={(checked) => {
                      setNesting(Boolean(checked));
                    }}
                  />
                  <Label htmlFor="ct-nesting" className="text-sm">
                    Nesting (Docker-in-LXC)
                  </Label>
                </div>
              </div>
            )}

            {/* Step 2: Template */}
            {step === "template" && (
              <div className="space-y-4">
                <div className="space-y-2">
                  <Label>Template Storage</Label>
                  <select
                    value={templateStorageId}
                    onChange={(e) => {
                      setTemplateStorageId(e.target.value);
                      setSelectedTemplate("");
                    }}
                    className={selectClass}
                  >
                    <option value="">Select storage</option>
                    {templateStoragePools.map((p) => (
                      <option key={p.id} value={p.id}>
                        {p.storage}
                      </option>
                    ))}
                  </select>
                </div>
                {templateStorageId && (
                  <div className="space-y-2">
                    <Label>Template</Label>
                    <select
                      value={selectedTemplate}
                      onChange={(e) => {
                        setSelectedTemplate(e.target.value);
                      }}
                      className={selectClass}
                    >
                      <option value="">Select template</option>
                      {templates.map((t) => (
                        <option key={t.volid} value={t.volid}>
                          {extractFilename(t.volid)} ({formatSize(t.size)})
                        </option>
                      ))}
                    </select>
                    {templates.length === 0 && (
                      <p className="text-xs text-muted-foreground">
                        No templates found on this storage. Upload a container
                        template first.
                      </p>
                    )}
                  </div>
                )}
                <div className="space-y-2">
                  <Label>Or enter template path manually</Label>
                  <Input
                    value={selectedTemplate}
                    onChange={(e) => {
                      setSelectedTemplate(e.target.value);
                    }}
                    placeholder="local:vztmpl/ubuntu-22.04-standard_22.04-1_amd64.tar.zst"
                  />
                  <p className="text-xs text-muted-foreground">
                    Full volume ID of the template (overrides selection above)
                  </p>
                </div>
              </div>
            )}

            {/* Step 3: Root Disk */}
            {step === "rootdisk" && (
              <div className="grid gap-4 sm:grid-cols-2">
                <div className="space-y-2 sm:col-span-2">
                  <Label>Storage Pool</Label>
                  <select
                    value={rootStorageId}
                    onChange={(e) => {
                      const id = e.target.value;
                      setRootStorageId(id);
                      const found = rootdirStoragePools.find(
                        (p) => p.id === id,
                      );
                      setRootStorageName(found ? found.storage : "");
                    }}
                    className={selectClass}
                  >
                    <option value="">Default</option>
                    {rootdirStoragePools.map((p) => (
                      <option key={p.id} value={p.id}>
                        {p.storage}
                      </option>
                    ))}
                  </select>
                </div>
                <div className="space-y-2">
                  <Label>Disk Size (GB)</Label>
                  <Input
                    type="number"
                    min={1}
                    value={diskSize}
                    onChange={(e) => {
                      setDiskSize(e.target.value);
                    }}
                  />
                </div>
              </div>
            )}

            {/* Step 4: CPU & Memory */}
            {step === "cpu_memory" && (
              <div className="grid gap-4 sm:grid-cols-2">
                <div className="space-y-2">
                  <Label>CPU Cores</Label>
                  <Input
                    type="number"
                    min={1}
                    value={cores}
                    onChange={(e) => {
                      setCores(e.target.value);
                    }}
                  />
                </div>
                <div className="space-y-2">
                  <Label>CPU Limit (optional)</Label>
                  <Input
                    type="number"
                    min={0}
                    step={0.5}
                    value={cpuLimit}
                    onChange={(e) => {
                      setCpuLimit(e.target.value);
                    }}
                    placeholder="e.g. 2.5"
                  />
                  <p className="text-xs text-muted-foreground">
                    Fractional CPU limit (empty = unlimited)
                  </p>
                </div>
                <div className="space-y-2">
                  <Label>CPU Units</Label>
                  <Input
                    type="number"
                    min={8}
                    max={500000}
                    value={cpuUnits}
                    onChange={(e) => {
                      setCpuUnits(e.target.value);
                    }}
                  />
                </div>
                <div className="space-y-2">
                  <Label>Architecture</Label>
                  <select
                    value={arch}
                    onChange={(e) => {
                      setArch(e.target.value);
                    }}
                    className={selectClass}
                  >
                    <option value="amd64">amd64</option>
                    <option value="i386">i386</option>
                    <option value="arm64">arm64</option>
                    <option value="armhf">armhf</option>
                  </select>
                </div>
                <div className="space-y-2">
                  <Label>Memory (MB)</Label>
                  <Input
                    type="number"
                    min={64}
                    step={64}
                    value={memory}
                    onChange={(e) => {
                      setMemory(e.target.value);
                    }}
                  />
                </div>
                <div className="space-y-2">
                  <Label>Swap (MB)</Label>
                  <Input
                    type="number"
                    min={0}
                    step={64}
                    value={swap}
                    onChange={(e) => {
                      setSwap(e.target.value);
                    }}
                  />
                </div>
              </div>
            )}

            {/* Step 5: Network */}
            {step === "network" && (
              <div className="grid gap-4 sm:grid-cols-2">
                <div className="space-y-2">
                  <Label>NIC Name</Label>
                  <Input
                    value={nicName}
                    onChange={(e) => {
                      setNicName(e.target.value);
                    }}
                    placeholder="eth0"
                  />
                </div>
                <div className="space-y-2">
                  <Label>Bridge</Label>
                  <Input
                    value={bridge}
                    onChange={(e) => {
                      setBridge(e.target.value);
                    }}
                    placeholder="vmbr0"
                  />
                </div>
                <div className="space-y-2">
                  <Label>MAC Address (optional)</Label>
                  <Input
                    value={macAddress}
                    onChange={(e) => {
                      setMacAddress(e.target.value);
                    }}
                    placeholder="auto"
                  />
                </div>
                <div className="space-y-2">
                  <Label>VLAN Tag (optional)</Label>
                  <Input
                    type="number"
                    min={1}
                    max={4094}
                    value={vlanTag}
                    onChange={(e) => {
                      setVlanTag(e.target.value);
                    }}
                  />
                </div>
                <div className="space-y-2">
                  <Label>Rate Limit MB/s (optional)</Label>
                  <Input
                    type="number"
                    min={0}
                    step={1}
                    value={rateLimit}
                    onChange={(e) => {
                      setRateLimit(e.target.value);
                    }}
                  />
                </div>
                <div className="space-y-2">
                  <Label>MTU (optional)</Label>
                  <Input
                    type="number"
                    min={576}
                    max={65535}
                    value={mtu}
                    onChange={(e) => {
                      setMtu(e.target.value);
                    }}
                  />
                </div>
                <div className="flex items-center gap-2 sm:col-span-2">
                  <Checkbox
                    id="ct-firewall"
                    checked={firewall}
                    onCheckedChange={(checked) => {
                      setFirewall(Boolean(checked));
                    }}
                  />
                  <Label htmlFor="ct-firewall" className="text-sm">
                    Firewall
                  </Label>
                </div>

                {/* IPv4 */}
                <div className="space-y-2 sm:col-span-2">
                  <Label>IPv4 Mode</Label>
                  <select
                    value={ipv4Mode}
                    onChange={(e) => {
                      setIpv4Mode(e.target.value as IPv4Mode);
                    }}
                    className={selectClass}
                  >
                    <option value="dhcp">DHCP</option>
                    <option value="static">Static</option>
                    <option value="none">None</option>
                  </select>
                </div>
                {ipv4Mode === "static" && (
                  <>
                    <div className="space-y-2">
                      <Label>IPv4 Address (CIDR)</Label>
                      <Input
                        value={ipv4Addr}
                        onChange={(e) => {
                          setIpv4Addr(e.target.value);
                        }}
                        placeholder="192.168.1.100/24"
                      />
                    </div>
                    <div className="space-y-2">
                      <Label>Gateway</Label>
                      <Input
                        value={ipv4Gw}
                        onChange={(e) => {
                          setIpv4Gw(e.target.value);
                        }}
                        placeholder="192.168.1.1"
                      />
                    </div>
                  </>
                )}

                {/* IPv6 */}
                <div className="space-y-2 sm:col-span-2">
                  <Label>IPv6 Mode</Label>
                  <select
                    value={ipv6Mode}
                    onChange={(e) => {
                      setIpv6Mode(e.target.value as IPv6Mode);
                    }}
                    className={selectClass}
                  >
                    <option value="none">None</option>
                    <option value="dhcp">DHCP</option>
                    <option value="slaac">SLAAC</option>
                    <option value="static">Static</option>
                  </select>
                </div>
                {ipv6Mode === "static" && (
                  <>
                    <div className="space-y-2">
                      <Label>IPv6 Address (CIDR)</Label>
                      <Input
                        value={ipv6Addr}
                        onChange={(e) => {
                          setIpv6Addr(e.target.value);
                        }}
                        placeholder="fd00::1/64"
                      />
                    </div>
                    <div className="space-y-2">
                      <Label>IPv6 Gateway</Label>
                      <Input
                        value={ipv6Gw}
                        onChange={(e) => {
                          setIpv6Gw(e.target.value);
                        }}
                        placeholder="fd00::1"
                      />
                    </div>
                  </>
                )}
              </div>
            )}

            {/* Step 6: DNS & Options */}
            {step === "dns_options" && (
              <div className="grid gap-4 sm:grid-cols-2">
                <div className="space-y-2">
                  <Label>DNS Domain (searchdomain)</Label>
                  <Input
                    value={searchdomain}
                    onChange={(e) => {
                      setSearchdomain(e.target.value);
                    }}
                    placeholder="example.com"
                  />
                </div>
                <div className="space-y-2">
                  <Label>DNS Server (nameserver)</Label>
                  <Input
                    value={nameserver}
                    onChange={(e) => {
                      setNameserver(e.target.value);
                    }}
                    placeholder="8.8.8.8"
                  />
                </div>
                <div className="space-y-2 sm:col-span-2">
                  <Label>Description</Label>
                  <textarea
                    value={description}
                    onChange={(e) => {
                      setDescription(e.target.value);
                    }}
                    rows={2}
                    className="flex w-full rounded-md border border-input bg-transparent px-3 py-2 text-sm shadow-sm placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
                    placeholder="Container description"
                  />
                </div>
                <div className="space-y-2">
                  <Label>Tags</Label>
                  <Input
                    value={tags}
                    onChange={(e) => {
                      setTags(e.target.value);
                    }}
                    placeholder="web,prod"
                  />
                  <p className="text-xs text-muted-foreground">
                    Comma-separated
                  </p>
                </div>
                <div className="space-y-2">
                  <Label>Startup Order</Label>
                  <Input
                    value={startup}
                    onChange={(e) => {
                      setStartup(e.target.value);
                    }}
                    placeholder="order=1,up=5,down=5"
                  />
                </div>
                <div className="space-y-2">
                  <Label>Console Mode</Label>
                  <select
                    value={cmode}
                    onChange={(e) => {
                      setCmode(e.target.value);
                    }}
                    className={selectClass}
                  >
                    <option value="tty">tty</option>
                    <option value="console">console</option>
                    <option value="shell">shell</option>
                  </select>
                </div>
                <div className="flex flex-col gap-3">
                  <div className="flex items-center gap-2">
                    <Checkbox
                      id="ct-onboot"
                      checked={onboot}
                      onCheckedChange={(checked) => {
                        setOnboot(Boolean(checked));
                      }}
                    />
                    <Label htmlFor="ct-onboot" className="text-sm">
                      Start on Boot
                    </Label>
                  </div>
                  <div className="flex items-center gap-2">
                    <Checkbox
                      id="ct-protection"
                      checked={protection}
                      onCheckedChange={(checked) => {
                        setProtection(Boolean(checked));
                      }}
                    />
                    <Label htmlFor="ct-protection" className="text-sm">
                      Protection
                    </Label>
                  </div>
                  <div className="flex items-center gap-2">
                    <Checkbox
                      id="ct-start-after"
                      checked={startAfter}
                      onCheckedChange={(checked) => {
                        setStartAfter(Boolean(checked));
                      }}
                    />
                    <Label htmlFor="ct-start-after" className="text-sm">
                      Start after creation
                    </Label>
                  </div>
                </div>
                <div className="space-y-2">
                  <Label>Password</Label>
                  <Input
                    type="password"
                    value={password}
                    onChange={(e) => {
                      setPassword(e.target.value);
                    }}
                    placeholder="Root password"
                  />
                </div>
                <div className="space-y-2">
                  <Label>Confirm Password</Label>
                  <Input
                    type="password"
                    value={passwordConfirm}
                    onChange={(e) => {
                      setPasswordConfirm(e.target.value);
                    }}
                    placeholder="Confirm password"
                  />
                  {passwordMismatch && (
                    <p className="text-xs text-destructive">
                      Passwords do not match
                    </p>
                  )}
                </div>
                <div className="space-y-2 sm:col-span-2">
                  <Label>SSH Public Keys</Label>
                  <textarea
                    value={sshKeys}
                    onChange={(e) => {
                      setSshKeys(e.target.value);
                    }}
                    rows={3}
                    className="flex w-full rounded-md border border-input bg-transparent px-3 py-2 text-sm shadow-sm placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring font-mono"
                    placeholder="ssh-ed25519 AAAA... user@host"
                  />
                </div>
              </div>
            )}

            {/* Step 7: Confirm */}
            {step === "confirm" && (
              <div className="space-y-3 text-sm">
                <div className="grid grid-cols-2 gap-x-4 gap-y-1 rounded-lg border p-3">
                  <span className="font-medium text-muted-foreground col-span-2 mb-1">
                    General
                  </span>
                  <span className="text-muted-foreground">VMID</span>
                  <span>{vmid}</span>
                  <span className="text-muted-foreground">Hostname</span>
                  <span>{hostname || "--"}</span>
                  <span className="text-muted-foreground">Node</span>
                  <span>{node}</span>
                  {pool && (
                    <>
                      <span className="text-muted-foreground">Pool</span>
                      <span>{pool}</span>
                    </>
                  )}
                  <span className="text-muted-foreground">Unprivileged</span>
                  <span>{unprivileged ? "Yes" : "No"}</span>
                  {nesting && (
                    <>
                      <span className="text-muted-foreground">Nesting</span>
                      <span>Enabled</span>
                    </>
                  )}
                </div>

                <div className="grid grid-cols-2 gap-x-4 gap-y-1 rounded-lg border p-3">
                  <span className="font-medium text-muted-foreground col-span-2 mb-1">
                    Template & Disk
                  </span>
                  <span className="text-muted-foreground">Template</span>
                  <span className="truncate">{selectedTemplate}</span>
                  <span className="text-muted-foreground">Root Disk</span>
                  <span>
                    {diskSize} GB{rootStorageName ? ` on ${rootStorageName}` : ""}
                  </span>
                </div>

                <div className="grid grid-cols-2 gap-x-4 gap-y-1 rounded-lg border p-3">
                  <span className="font-medium text-muted-foreground col-span-2 mb-1">
                    CPU & Memory
                  </span>
                  <span className="text-muted-foreground">Cores</span>
                  <span>{cores}</span>
                  {cpuLimit && (
                    <>
                      <span className="text-muted-foreground">CPU Limit</span>
                      <span>{cpuLimit}</span>
                    </>
                  )}
                  {arch !== "amd64" && (
                    <>
                      <span className="text-muted-foreground">Arch</span>
                      <span>{arch}</span>
                    </>
                  )}
                  <span className="text-muted-foreground">Memory</span>
                  <span>{memory} MB</span>
                  <span className="text-muted-foreground">Swap</span>
                  <span>{swap} MB</span>
                </div>

                <div className="grid grid-cols-2 gap-x-4 gap-y-1 rounded-lg border p-3">
                  <span className="font-medium text-muted-foreground col-span-2 mb-1">
                    Network
                  </span>
                  <span className="text-muted-foreground">NIC</span>
                  <span>
                    {nicName} on {bridge}
                  </span>
                  <span className="text-muted-foreground">IPv4</span>
                  <span>
                    {ipv4Mode === "dhcp"
                      ? "DHCP"
                      : ipv4Mode === "static"
                        ? ipv4Addr
                        : "None"}
                  </span>
                  {ipv6Mode !== "none" && (
                    <>
                      <span className="text-muted-foreground">IPv6</span>
                      <span>
                        {ipv6Mode === "dhcp"
                          ? "DHCP"
                          : ipv6Mode === "slaac"
                            ? "SLAAC"
                            : ipv6Addr}
                      </span>
                    </>
                  )}
                  {vlanTag && (
                    <>
                      <span className="text-muted-foreground">VLAN</span>
                      <span>{vlanTag}</span>
                    </>
                  )}
                  {firewall && (
                    <>
                      <span className="text-muted-foreground">Firewall</span>
                      <span>Enabled</span>
                    </>
                  )}
                </div>

                {(nameserver ||
                  searchdomain ||
                  description ||
                  tags ||
                  onboot ||
                  protection ||
                  startup ||
                  password) && (
                  <div className="grid grid-cols-2 gap-x-4 gap-y-1 rounded-lg border p-3">
                    <span className="font-medium text-muted-foreground col-span-2 mb-1">
                      Options
                    </span>
                    {nameserver && (
                      <>
                        <span className="text-muted-foreground">
                          DNS Server
                        </span>
                        <span>{nameserver}</span>
                      </>
                    )}
                    {searchdomain && (
                      <>
                        <span className="text-muted-foreground">
                          DNS Domain
                        </span>
                        <span>{searchdomain}</span>
                      </>
                    )}
                    {description && (
                      <>
                        <span className="text-muted-foreground">
                          Description
                        </span>
                        <span className="truncate">{description}</span>
                      </>
                    )}
                    {tags && (
                      <>
                        <span className="text-muted-foreground">Tags</span>
                        <span>{tags}</span>
                      </>
                    )}
                    {onboot && (
                      <>
                        <span className="text-muted-foreground">
                          Start on Boot
                        </span>
                        <span>Yes</span>
                      </>
                    )}
                    {protection && (
                      <>
                        <span className="text-muted-foreground">
                          Protection
                        </span>
                        <span>Yes</span>
                      </>
                    )}
                    {startup && (
                      <>
                        <span className="text-muted-foreground">
                          Startup Order
                        </span>
                        <span>{startup}</span>
                      </>
                    )}
                    {password && (
                      <>
                        <span className="text-muted-foreground">Password</span>
                        <span>Set</span>
                      </>
                    )}
                    {sshKeys && (
                      <>
                        <span className="text-muted-foreground">SSH Keys</span>
                        <span>Provided</span>
                      </>
                    )}
                  </div>
                )}

                <div className="grid grid-cols-2 gap-x-4 gap-y-1 rounded-lg border p-3">
                  <span className="text-muted-foreground">Start after</span>
                  <span>{startAfter ? "Yes" : "No"}</span>
                </div>
              </div>
            )}

            {createMutation.isError && (
              <p className="text-sm text-destructive">
                {createMutation.error.message}
              </p>
            )}

            <DialogFooter>
              <Button type="button" variant="outline" onClick={handleClose}>
                Cancel
              </Button>
              {stepIdx > 0 && (
                <Button
                  type="button"
                  variant="outline"
                  onClick={() => {
                    const prev = steps[stepIdx - 1];
                    if (prev) setStep(prev);
                  }}
                >
                  Back
                </Button>
              )}
              {step !== "confirm" ? (
                <Button
                  type="button"
                  disabled={!canProceed}
                  onClick={() => {
                    const next = steps[stepIdx + 1];
                    if (next) setStep(next);
                  }}
                >
                  Next
                </Button>
              ) : (
                <Button
                  type="button"
                  disabled={createMutation.isPending}
                  onClick={handleSubmit}
                >
                  {createMutation.isPending
                    ? "Creating..."
                    : "Create Container"}
                </Button>
              )}
            </DialogFooter>
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}
