import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  CheckField,
  Field,
  FieldGrid,
  FormSection,
  NativeSelect,
  TextArea,
} from "@/components/page/Field";
import { PropertyList, PropertyRow } from "@/components/page/Panel";
import { Wizard, type WizardStep } from "@/components/page/Wizard";
import { WarningCallout } from "@/components/WarningCallout";
import { formatBytes } from "@/lib/format";
import { cn } from "@/lib/utils";
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
import { useSaveOutcome } from "@/hooks/useSaveOutcome";
import { usePermissions } from "@/hooks/usePermissions";
import { TaskProgressBanner } from "./TaskProgressBanner";
import { filterStorageByContent } from "../lib/storage-filter";

interface CreateCTDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  clusterId: string;
}

type Step =
  | "general"
  | "template"
  | "rootdisk"
  | "cpu_memory"
  | "network"
  | "dns_options"
  | "confirm";

const steps: readonly WizardStep<Step>[] = [
  {
    id: "general",
    label: "General",
    hint: "What the container is called and where it runs.",
  },
  {
    id: "template",
    label: "Template",
    hint: "The image the container is built from.",
  },
  {
    id: "rootdisk",
    label: "Root disk",
    hint: "Where the root filesystem lives and how large it is.",
  },
  {
    id: "cpu_memory",
    label: "CPU and memory",
    hint: "The processor and memory the container may use.",
  },
  {
    id: "network",
    label: "Network",
    hint: "The container's first network interface and its addresses.",
  },
  {
    id: "dns_options",
    label: "DNS and options",
    hint: "Name resolution, startup behaviour and the root login.",
  },
  {
    id: "confirm",
    label: "Review",
    hint: "Check the settings, then create the container.",
  },
];

type IPv4Mode = "dhcp" | "static" | "none";
type IPv6Mode = "dhcp" | "slaac" | "static" | "none";

export function CreateCTDialog({
  open,
  onOpenChange,
  clusterId,
}: CreateCTDialogProps) {
  const { t } = useTranslation("vms");
  const { data: nodes } = useClusterNodes(clusterId);
  const { data: storageList, isPending: storagePending } =
    useClusterStorage(clusterId);
  const { data: usedVMIDs } = useClusterVMIDs(clusterId);
  const { data: pools } = useResourcePools(clusterId);
  const { data: clusterVMs } = useClusterVMs(clusterId);
  const createMutation = useCreateContainer();
  const settle = useSaveOutcome(open);

  // Template storage selection for browsing vztmpl content
  // Target node name (Step 1). Declared up here so the storage filters below
  // can react to it.
  const [node, setNode] = useState("");

  const [templateStorageId, setTemplateStorageId] = useState("");

  // Storages are filtered by both content type AND target-node reachability.
  // A storage is reachable from the target node iff it's marked shared (visible
  // on every node) or its node_id matches the selected target node. Without this
  // node filter, deduping by name can surface a per-node-local storage (e.g.
  // `local-lvm`) configured on a different node, then container create fails at
  // submit time because Proxmox can't find that storage on the target node.

  const templateStoragePools = useMemo(
    () => filterStorageByContent(storageList, "vztmpl", nodes, node),
    [storageList, nodes, node],
  );
  // A store pick the chosen node does not have counts as none: its list was
  // read on the node that has it (by row id), and a template chosen from it
  // goes with it.
  const templateStore = templateStoragePools.some(
    (p) => p.id === templateStorageId,
  )
    ? templateStorageId
    : "";
  const { data: templateContent } = useStorageContent(clusterId, templateStore);

  const templates = useMemo(() => {
    if (!templateContent) return [];
    return templateContent
      .filter((item) => item.content === "vztmpl")
      .sort((a, b) => b.ctime - a.ctime);
  }, [templateContent]);

  const rootdirStoragePools = useMemo(
    () => filterStorageByContent(storageList, "rootdir", nodes, node),
    [storageList, nodes, node],
  );

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
      const cpuFree =
        n.cpu_count > 0 ? (n.cpu_count - alloc.cpu) / n.cpu_count : 0;
      const memFree =
        n.mem_total > 0 ? (n.mem_total - alloc.mem) / n.mem_total : 0;
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
  const [pool, setPool] = useState("");
  const [unprivileged, setUnprivileged] = useState(true);
  const [nesting, setNesting] = useState(false);

  // Step 2: Template
  const [selectedTemplate, setSelectedTemplate] = useState("");
  // Typed as a path, a template names its own store and stays whatever the
  // node; chosen from the list, it is sent only while its store is on the node.
  const [templateTyped, setTemplateTyped] = useState(false);
  const ostemplate = templateTyped || templateStore ? selectedTemplate : "";

  // Step 3: Root Disk
  // The root disk's store, by name (a node's own store has a row per node):
  // the pick where this node has it, else the node's first that holds
  // container disks. "Default" sent neither storage nor rootfs, and Proxmox
  // fell back to `local`, which normally refuses them.
  const [rootStorageName, setRootStorageName] = useState("");
  const rootStore =
    rootdirStoragePools.find((p) => p.storage === rootStorageName) ??
    rootdirStoragePools[0];
  const noRootStore = !storagePending && rootStore === undefined;
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
  // The server holds a create's start to what the power route needs (execute
  // on the kind), so the tick is offered only to a holder, as RestoreDialog's.
  const { canExecute } = usePermissions();
  const canStart = canExecute("container");
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
  const stepIdx = steps.findIndex((s) => s.id === step);

  function extractFilename(volid: string): string {
    // volid like "local:vztmpl/ubuntu-22.04-standard_22.04-1_amd64.tar.zst"
    const parts = volid.split("/");
    return parts.length > 1 ? (parts[parts.length - 1] ?? volid) : volid;
  }

  // Heuristic: OCI archives are stored as plain `.tar` (no compression). Standard
  // pveam templates are `.tar.gz`, `.tar.xz`, or `.tar.zst`. Used to flag OCI rows
  // in the picker — not authoritative, just a UX hint.
  function looksLikeOCI(volid: string): boolean {
    const name = extractFilename(volid).toLowerCase();
    return name.endsWith(".tar");
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
    const rootName = rootStore?.storage ?? "";
    const rootfs = rootName && diskSize ? `${rootName}:${diskSize}` : "";

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

    settle(
      createMutation.mutateAsync({
        clusterId,
        body: {
          vmid: Number(vmid),
          hostname,
          node,
          ostemplate,
          storage: rootName,
          rootfs,
          memory: Number(memory),
          swap: Number(swap),
          cores: Number(cores),
          net0: buildNet0(),
          password,
          ssh_keys: sshKeys,
          unprivileged,
          start: startAfter && canStart,
          description: description || undefined,
          tags: tags || undefined,
          pool: pool || undefined,
          nameserver: nameserver || undefined,
          searchdomain: searchdomain || undefined,
          extra: Object.keys(extra).length > 0 ? extra : undefined,
        },
      }),
      {
        action: `Creating container ${hostname || vmid}`,
        onSuccess: (data) => {
          setUpid(data.upid);
        },
        onError: () => undefined, // shown below from the mutation's state
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
    setTemplateTyped(false);
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

  function stepValid(s: Step): boolean {
    switch (s) {
      case "general":
        return Number(vmid) > 0 && node !== "";
      case "template":
        return ostemplate !== "";
      case "rootdisk":
        return Number(diskSize) > 0 && rootStore !== undefined;
      case "cpu_memory":
        return Number(cores) > 0 && Number(memory) > 0;
      case "network":
        return nicName !== "" && bridge !== "";
      case "dns_options":
        return !passwordMismatch;
      default:
        return true;
    }
  }

  const canProceed = stepValid(step);

  // The step list goes back freely, and ahead only as far as Next would: every
  // step between here and there has to pass the check Next holds it to.
  function canGoTo(target: Step): boolean {
    const targetIdx = steps.findIndex((s) => s.id === target);
    return steps.slice(stepIdx, targetIdx).every((s) => stepValid(s.id));
  }

  /** The Review step's way back to the step a group of settings came from. */
  const editStep = (target: Step) => (
    <Button
      type="button"
      variant="ghost"
      size="sm"
      className="h-6 px-2 text-primary hover:text-primary"
      aria-label={`Edit ${steps.find((s) => s.id === target)?.label ?? target}`}
      onClick={() => {
        setStep(target);
      }}
    >
      Edit
    </Button>
  );

  return (
    <Dialog open={open} onOpenChange={handleClose}>
      <DialogContent
        className={cn(
          "flex max-h-[85vh] flex-col gap-0 overflow-hidden p-0",
          upid ? "max-w-lg" : "h-[640px] max-w-3xl",
        )}
      >
        <DialogHeader className="border-b px-5 py-4 pr-12 sm:px-6">
          <DialogTitle>Create container</DialogTitle>
          {/* What is being built so far, so the header reads as its identity. */}
          <DialogDescription>
            CT {vmid || "—"}
            {hostname ? `, ${hostname}` : ""}
            {node ? `, on ${node}` : ""}
          </DialogDescription>
        </DialogHeader>

        {upid ? (
          <div className="px-5 py-5 sm:px-6">
            <TaskProgressBanner
              clusterId={clusterId}
              upid={upid}
              onComplete={(success) => {
                // A failed create stays open on the banner, which says why.
                if (success) handleClose();
              }}
              description={`Create CT ${hostname || vmid}`}
            />
          </div>
        ) : (
          <Wizard
            steps={steps}
            current={step}
            onStepChange={setStep}
            canGoTo={canGoTo}
            notice={
              createMutation.isError ? (
                <p className="text-sm text-destructive">
                  {createMutation.error.message}
                </p>
              ) : (
                // Why Next is held, said where it shows: the field that says
                // it sits below the fold of this step.
                (step === "dns_options" && passwordMismatch && (
                  <p className="text-sm text-destructive">
                    Passwords do not match
                  </p>
                )) ||
                (step === "rootdisk" && noRootStore && (
                  <p className="text-sm text-muted-foreground">
                    No storage for container disks is available on{" "}
                    {node || "this cluster"}.
                  </p>
                ))
              )
            }
            footer={
              <>
                <Button
                  type="button"
                  variant="ghost"
                  className="mr-auto"
                  onClick={handleClose}
                >
                  Cancel
                </Button>
                {stepIdx > 0 && (
                  <Button
                    type="button"
                    variant="outline"
                    onClick={() => {
                      const prev = steps[stepIdx - 1];
                      if (prev) setStep(prev.id);
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
                      if (next) setStep(next.id);
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
                      : "Create container"}
                  </Button>
                )}
              </>
            }
          >
            {/* Step 1: General */}
            {step === "general" && (
              <FieldGrid>
                <Field
                  label="VMID"
                  htmlFor="ct-vmid"
                  warning={isDuplicate && "VMID may already be in use"}
                >
                  <Input
                    id="ct-vmid"
                    type="number"
                    min={1}
                    value={vmid}
                    onChange={(e) => {
                      setVmid(e.target.value);
                    }}
                  />
                </Field>
                <Field label="Hostname" htmlFor="ct-hostname">
                  <Input
                    id="ct-hostname"
                    value={hostname}
                    onChange={(e) => {
                      setHostname(e.target.value);
                    }}
                    placeholder="my-container"
                  />
                </Field>
                <Field label="Node" htmlFor="ct-node">
                  <NativeSelect
                    id="ct-node"
                    value={node}
                    onChange={(e) => {
                      setNode(e.target.value);
                    }}
                  >
                    <option value="">Select node</option>
                    {nodes?.map((n) => (
                      <option key={n.id} value={n.name}>
                        {n.name}
                      </option>
                    ))}
                  </NativeSelect>
                </Field>
                <Field label="Resource pool" htmlFor="ct-pool">
                  <NativeSelect
                    id="ct-pool"
                    value={pool}
                    onChange={(e) => {
                      setPool(e.target.value);
                    }}
                  >
                    <option value="">None</option>
                    {pools?.map((p) => (
                      <option key={p.poolid} value={p.poolid}>
                        {p.poolid}
                        {p.comment ? ` — ${p.comment}` : ""}
                      </option>
                    ))}
                  </NativeSelect>
                </Field>
                <CheckField
                  id="ct-unprivileged"
                  label="Unprivileged container"
                  hint="Root inside the container is an ordinary user on the node."
                  checked={unprivileged}
                  onCheckedChange={setUnprivileged}
                />
                <CheckField
                  id="ct-nesting"
                  label="Nesting"
                  hint="Lets the container run containers of its own, such as Docker."
                  checked={nesting}
                  onCheckedChange={setNesting}
                />
              </FieldGrid>
            )}

            {/* Step 2: Template */}
            {step === "template" && (
              <div className="space-y-4">
                <Field label="Template storage" htmlFor="ct-template-storage">
                  <NativeSelect
                    id="ct-template-storage"
                    value={templateStore}
                    onChange={(e) => {
                      setTemplateStorageId(e.target.value);
                      setSelectedTemplate("");
                      setTemplateTyped(false);
                    }}
                  >
                    <option value="">Select storage</option>
                    {templateStoragePools.map((p) => (
                      <option key={p.id} value={p.id}>
                        {p.storage}
                      </option>
                    ))}
                  </NativeSelect>
                </Field>
                {templateStore && (
                  <Field
                    label="Template"
                    htmlFor="ct-template"
                    hint={
                      templates.length === 0 &&
                      "No templates found on this storage. Upload a container template first."
                    }
                  >
                    <NativeSelect
                      id="ct-template"
                      value={selectedTemplate}
                      onChange={(e) => {
                        setSelectedTemplate(e.target.value);
                        setTemplateTyped(false);
                      }}
                    >
                      <option value="">Select template</option>
                      {templates.map((t) => (
                        <option key={t.volid} value={t.volid}>
                          {extractFilename(t.volid)} ({formatBytes(t.size)})
                          {looksLikeOCI(t.volid) ? "  — OCI (preview)" : ""}
                        </option>
                      ))}
                    </NativeSelect>
                  </Field>
                )}
                {ostemplate && looksLikeOCI(ostemplate) && (
                  <WarningCallout title="OCI image (tech preview)">
                    <p>
                      Layers are squashed on create. In-place updates are not
                      supported: re-create the container to apply image changes.
                    </p>
                  </WarningCallout>
                )}
                <Field
                  label="Template path"
                  htmlFor="ct-template-path"
                  hint="Optional. A full volume ID typed here replaces the selection above."
                >
                  <Input
                    id="ct-template-path"
                    value={ostemplate}
                    onChange={(e) => {
                      setSelectedTemplate(e.target.value);
                      setTemplateTyped(true);
                    }}
                    placeholder="local:vztmpl/ubuntu-22.04-standard_22.04-1_amd64.tar.zst"
                  />
                </Field>
              </div>
            )}

            {/* Step 3: Root Disk */}
            {step === "rootdisk" && (
              <FieldGrid>
                <Field label="Storage" htmlFor="ct-root-storage">
                  <NativeSelect
                    id="ct-root-storage"
                    value={rootStore?.storage ?? ""}
                    onChange={(e) => {
                      setRootStorageName(e.target.value);
                    }}
                  >
                    <option value="" disabled>
                      Select storage
                    </option>
                    {rootdirStoragePools.map((p) => (
                      <option key={p.storage} value={p.storage}>
                        {p.storage}
                      </option>
                    ))}
                  </NativeSelect>
                </Field>
                <Field label="Disk size (GiB)" htmlFor="ct-disk-size">
                  <Input
                    id="ct-disk-size"
                    type="number"
                    min={1}
                    value={diskSize}
                    onChange={(e) => {
                      setDiskSize(e.target.value);
                    }}
                  />
                </Field>
              </FieldGrid>
            )}

            {/* Step 4: CPU & Memory */}
            {step === "cpu_memory" && (
              <div className="space-y-4">
                <FormSection title="Processor">
                  <FieldGrid>
                    <Field label="Cores" htmlFor="ct-cores">
                      <Input
                        id="ct-cores"
                        type="number"
                        min={1}
                        value={cores}
                        onChange={(e) => {
                          setCores(e.target.value);
                        }}
                      />
                    </Field>
                    <Field
                      label="CPU limit"
                      htmlFor="ct-cpu-limit"
                      hint="Optional. A fraction of cores, such as 2.5. Blank means no limit."
                    >
                      <Input
                        id="ct-cpu-limit"
                        type="number"
                        min={0}
                        step={0.5}
                        value={cpuLimit}
                        onChange={(e) => {
                          setCpuLimit(e.target.value);
                        }}
                        placeholder="Unlimited"
                      />
                    </Field>
                    <Field label="CPU units" htmlFor="ct-cpu-units">
                      <Input
                        id="ct-cpu-units"
                        type="number"
                        min={8}
                        max={500000}
                        value={cpuUnits}
                        onChange={(e) => {
                          setCpuUnits(e.target.value);
                        }}
                      />
                    </Field>
                    <Field label="Architecture" htmlFor="ct-arch">
                      <NativeSelect
                        id="ct-arch"
                        value={arch}
                        onChange={(e) => {
                          setArch(e.target.value);
                        }}
                      >
                        <option value="amd64">amd64</option>
                        <option value="i386">i386</option>
                        <option value="arm64">arm64</option>
                        <option value="armhf">armhf</option>
                      </NativeSelect>
                    </Field>
                  </FieldGrid>
                </FormSection>
                <FormSection title="Memory">
                  <FieldGrid>
                    <Field label="Memory (MiB)" htmlFor="ct-memory">
                      <Input
                        id="ct-memory"
                        type="number"
                        min={64}
                        step={64}
                        value={memory}
                        onChange={(e) => {
                          setMemory(e.target.value);
                        }}
                      />
                    </Field>
                    <Field label="Swap (MiB)" htmlFor="ct-swap">
                      <Input
                        id="ct-swap"
                        type="number"
                        min={0}
                        step={64}
                        value={swap}
                        onChange={(e) => {
                          setSwap(e.target.value);
                        }}
                      />
                    </Field>
                  </FieldGrid>
                </FormSection>
              </div>
            )}

            {/* Step 5: Network */}
            {step === "network" && (
              <div className="space-y-4">
                <FormSection title="Interface">
                  <FieldGrid>
                    <Field label="Name" htmlFor="ct-nic-name">
                      <Input
                        id="ct-nic-name"
                        value={nicName}
                        onChange={(e) => {
                          setNicName(e.target.value);
                        }}
                        placeholder="eth0"
                      />
                    </Field>
                    <Field label="Bridge" htmlFor="ct-bridge">
                      <Input
                        id="ct-bridge"
                        value={bridge}
                        onChange={(e) => {
                          setBridge(e.target.value);
                        }}
                        placeholder="vmbr0"
                      />
                    </Field>
                    <Field label="MAC address" htmlFor="ct-mac">
                      <Input
                        id="ct-mac"
                        value={macAddress}
                        onChange={(e) => {
                          setMacAddress(e.target.value);
                        }}
                        placeholder="Auto-generated"
                      />
                    </Field>
                    <Field label="VLAN tag" htmlFor="ct-vlan">
                      <Input
                        id="ct-vlan"
                        type="number"
                        min={1}
                        max={4094}
                        value={vlanTag}
                        onChange={(e) => {
                          setVlanTag(e.target.value);
                        }}
                        placeholder="None"
                      />
                    </Field>
                    <Field label="Rate limit (MB/s)" htmlFor="ct-rate">
                      <Input
                        id="ct-rate"
                        type="number"
                        min={0}
                        step={1}
                        value={rateLimit}
                        onChange={(e) => {
                          setRateLimit(e.target.value);
                        }}
                        placeholder="Unlimited"
                      />
                    </Field>
                    <Field label="MTU" htmlFor="ct-mtu">
                      <Input
                        id="ct-mtu"
                        type="number"
                        min={576}
                        max={65535}
                        value={mtu}
                        onChange={(e) => {
                          setMtu(e.target.value);
                        }}
                        placeholder="Default"
                      />
                    </Field>
                    <CheckField
                      id="ct-firewall"
                      label="Firewall"
                      hint="Applies the container's firewall rules to this interface."
                      checked={firewall}
                      onCheckedChange={setFirewall}
                      className="sm:col-span-2"
                    />
                  </FieldGrid>
                </FormSection>

                <FormSection title="IPv4">
                  <FieldGrid>
                    <Field
                      label="Mode"
                      htmlFor="ct-ipv4-mode"
                      className="sm:col-span-2"
                    >
                      <NativeSelect
                        id="ct-ipv4-mode"
                        value={ipv4Mode}
                        onChange={(e) => {
                          setIpv4Mode(e.target.value as IPv4Mode);
                        }}
                      >
                        <option value="dhcp">DHCP</option>
                        <option value="static">Static</option>
                        <option value="none">None</option>
                      </NativeSelect>
                    </Field>
                    {ipv4Mode === "static" && (
                      <>
                        <Field label="Address (CIDR)" htmlFor="ct-ipv4-addr">
                          <Input
                            id="ct-ipv4-addr"
                            value={ipv4Addr}
                            onChange={(e) => {
                              setIpv4Addr(e.target.value);
                            }}
                            placeholder="192.0.2.10/24"
                          />
                        </Field>
                        <Field label="Gateway" htmlFor="ct-ipv4-gw">
                          <Input
                            id="ct-ipv4-gw"
                            value={ipv4Gw}
                            onChange={(e) => {
                              setIpv4Gw(e.target.value);
                            }}
                            placeholder="192.0.2.1"
                          />
                        </Field>
                      </>
                    )}
                  </FieldGrid>
                </FormSection>

                <FormSection title="IPv6">
                  <FieldGrid>
                    <Field
                      label="Mode"
                      htmlFor="ct-ipv6-mode"
                      className="sm:col-span-2"
                    >
                      <NativeSelect
                        id="ct-ipv6-mode"
                        value={ipv6Mode}
                        onChange={(e) => {
                          setIpv6Mode(e.target.value as IPv6Mode);
                        }}
                      >
                        <option value="none">None</option>
                        <option value="dhcp">DHCP</option>
                        <option value="slaac">SLAAC</option>
                        <option value="static">Static</option>
                      </NativeSelect>
                    </Field>
                    {ipv6Mode === "static" && (
                      <>
                        <Field label="Address (CIDR)" htmlFor="ct-ipv6-addr">
                          <Input
                            id="ct-ipv6-addr"
                            value={ipv6Addr}
                            onChange={(e) => {
                              setIpv6Addr(e.target.value);
                            }}
                            placeholder="2001:db8::10/64"
                          />
                        </Field>
                        <Field label="Gateway" htmlFor="ct-ipv6-gw">
                          <Input
                            id="ct-ipv6-gw"
                            value={ipv6Gw}
                            onChange={(e) => {
                              setIpv6Gw(e.target.value);
                            }}
                            placeholder="2001:db8::1"
                          />
                        </Field>
                      </>
                    )}
                  </FieldGrid>
                </FormSection>
              </div>
            )}

            {/* Step 6: DNS & Options */}
            {step === "dns_options" && (
              <div className="space-y-4">
                <FormSection title="DNS">
                  <FieldGrid>
                    <Field label="Search domain" htmlFor="ct-searchdomain">
                      <Input
                        id="ct-searchdomain"
                        value={searchdomain}
                        onChange={(e) => {
                          setSearchdomain(e.target.value);
                        }}
                        placeholder="example.com"
                      />
                    </Field>
                    <Field label="DNS server" htmlFor="ct-nameserver">
                      <Input
                        id="ct-nameserver"
                        value={nameserver}
                        onChange={(e) => {
                          setNameserver(e.target.value);
                        }}
                        placeholder="192.0.2.53"
                      />
                    </Field>
                  </FieldGrid>
                </FormSection>

                <FormSection title="Notes">
                  <FieldGrid>
                    <Field
                      label="Description"
                      htmlFor="ct-description"
                      className="sm:col-span-2"
                    >
                      <TextArea
                        id="ct-description"
                        value={description}
                        onChange={(e) => {
                          setDescription(e.target.value);
                        }}
                        rows={2}
                        placeholder="Container description"
                      />
                    </Field>
                    <Field
                      label="Tags"
                      htmlFor="ct-tags"
                      hint="Separated by commas."
                      className="sm:col-span-2"
                    >
                      <Input
                        id="ct-tags"
                        value={tags}
                        onChange={(e) => {
                          setTags(e.target.value);
                        }}
                        placeholder="web,prod"
                      />
                    </Field>
                  </FieldGrid>
                </FormSection>

                <FormSection title="Startup">
                  <FieldGrid>
                    <Field label="Startup order" htmlFor="ct-startup">
                      <Input
                        id="ct-startup"
                        value={startup}
                        onChange={(e) => {
                          setStartup(e.target.value);
                        }}
                        placeholder="order=1,up=5,down=5"
                      />
                    </Field>
                    <Field label="Console mode" htmlFor="ct-cmode">
                      <NativeSelect
                        id="ct-cmode"
                        value={cmode}
                        onChange={(e) => {
                          setCmode(e.target.value);
                        }}
                      >
                        <option value="tty">tty</option>
                        <option value="console">console</option>
                        <option value="shell">shell</option>
                      </NativeSelect>
                    </Field>
                  </FieldGrid>
                  <div className="flex flex-wrap gap-x-6 gap-y-2">
                    <CheckField
                      id="ct-onboot"
                      label="Start on boot"
                      checked={onboot}
                      onCheckedChange={setOnboot}
                    />
                    <CheckField
                      id="ct-start-after"
                      label="Start after creation"
                      checked={startAfter && canStart}
                      disabled={!canStart}
                      hint={
                        canStart
                          ? undefined
                          : t("startNeedsExecute", {
                              permission: "execute:container",
                            })
                      }
                      onCheckedChange={setStartAfter}
                    />
                    <CheckField
                      id="ct-protection"
                      label="Protection"
                      checked={protection}
                      onCheckedChange={setProtection}
                    />
                  </div>
                </FormSection>

                <FormSection title="Root login">
                  <FieldGrid>
                    <Field label="Password" htmlFor="ct-password">
                      <Input
                        id="ct-password"
                        type="password"
                        value={password}
                        onChange={(e) => {
                          setPassword(e.target.value);
                        }}
                        placeholder="Root password"
                      />
                    </Field>
                    <Field
                      label="Confirm password"
                      htmlFor="ct-password-confirm"
                      error={passwordMismatch && "Passwords do not match"}
                    >
                      <Input
                        id="ct-password-confirm"
                        type="password"
                        value={passwordConfirm}
                        onChange={(e) => {
                          setPasswordConfirm(e.target.value);
                        }}
                        placeholder="Confirm password"
                      />
                    </Field>
                    <Field
                      label="SSH public keys"
                      htmlFor="ct-sshkeys"
                      hint="One key per line."
                      className="sm:col-span-2"
                    >
                      <TextArea
                        id="ct-sshkeys"
                        value={sshKeys}
                        onChange={(e) => {
                          setSshKeys(e.target.value);
                        }}
                        rows={3}
                        className="min-h-[80px] font-mono"
                        placeholder="ssh-ed25519 AAAA... user@host"
                      />
                    </Field>
                  </FieldGrid>
                </FormSection>
              </div>
            )}

            {/* Step 7: Review */}
            {step === "confirm" && (
              <div className="space-y-4">
                <FormSection title="General" action={editStep("general")}>
                  <PropertyList>
                    <PropertyRow label="VMID">{vmid}</PropertyRow>
                    <PropertyRow label="Hostname">
                      {hostname || "—"}
                    </PropertyRow>
                    <PropertyRow label="Node">{node}</PropertyRow>
                    {pool && <PropertyRow label="Pool">{pool}</PropertyRow>}
                    <PropertyRow label="Unprivileged">
                      {unprivileged ? "Yes" : "No"}
                    </PropertyRow>
                    {nesting && (
                      <PropertyRow label="Nesting">Enabled</PropertyRow>
                    )}
                  </PropertyList>
                </FormSection>

                <FormSection
                  title="Template and disk"
                  action={editStep("template")}
                >
                  <PropertyList>
                    <PropertyRow label="Template">{ostemplate}</PropertyRow>
                    <PropertyRow label="Root disk">
                      {diskSize} GiB
                      {rootStore ? ` on ${rootStore.storage}` : ""}
                    </PropertyRow>
                  </PropertyList>
                </FormSection>

                <FormSection
                  title="CPU and memory"
                  action={editStep("cpu_memory")}
                >
                  <PropertyList>
                    <PropertyRow label="Cores">{cores}</PropertyRow>
                    {cpuLimit && (
                      <PropertyRow label="CPU limit">{cpuLimit}</PropertyRow>
                    )}
                    {arch !== "amd64" && (
                      <PropertyRow label="Architecture">{arch}</PropertyRow>
                    )}
                    <PropertyRow label="Memory">{memory} MiB</PropertyRow>
                    <PropertyRow label="Swap">{swap} MiB</PropertyRow>
                  </PropertyList>
                </FormSection>

                <FormSection title="Network" action={editStep("network")}>
                  <PropertyList>
                    <PropertyRow label="Interface">
                      {nicName} on {bridge}
                    </PropertyRow>
                    <PropertyRow label="IPv4">
                      {ipv4Mode === "dhcp"
                        ? "DHCP"
                        : ipv4Mode === "static"
                          ? ipv4Addr
                          : "None"}
                    </PropertyRow>
                    {ipv6Mode !== "none" && (
                      <PropertyRow label="IPv6">
                        {ipv6Mode === "dhcp"
                          ? "DHCP"
                          : ipv6Mode === "slaac"
                            ? "SLAAC"
                            : ipv6Addr}
                      </PropertyRow>
                    )}
                    {vlanTag && (
                      <PropertyRow label="VLAN">{vlanTag}</PropertyRow>
                    )}
                    {firewall && (
                      <PropertyRow label="Firewall">Enabled</PropertyRow>
                    )}
                  </PropertyList>
                </FormSection>

                <FormSection title="Options" action={editStep("dns_options")}>
                  <PropertyList>
                    {nameserver && (
                      <PropertyRow label="DNS server">{nameserver}</PropertyRow>
                    )}
                    {searchdomain && (
                      <PropertyRow label="Search domain">
                        {searchdomain}
                      </PropertyRow>
                    )}
                    {description && (
                      <PropertyRow label="Description">
                        {description}
                      </PropertyRow>
                    )}
                    {tags && <PropertyRow label="Tags">{tags}</PropertyRow>}
                    {onboot && (
                      <PropertyRow label="Start on boot">Yes</PropertyRow>
                    )}
                    {protection && (
                      <PropertyRow label="Protection">Yes</PropertyRow>
                    )}
                    {startup && (
                      <PropertyRow label="Startup order">{startup}</PropertyRow>
                    )}
                    {password && (
                      <PropertyRow label="Password">Set</PropertyRow>
                    )}
                    {sshKeys && (
                      <PropertyRow label="SSH keys">Provided</PropertyRow>
                    )}
                    <PropertyRow label="Start after creation">
                      {startAfter && canStart ? "Yes" : "No"}
                    </PropertyRow>
                  </PropertyList>
                </FormSection>
              </div>
            )}
          </Wizard>
        )}
      </DialogContent>
    </Dialog>
  );
}
