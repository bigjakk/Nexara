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
} from "@/features/clusters/api/cluster-queries";
import { useClusterVMIDs, useCreateContainer } from "../api/vm-queries";
import { TaskProgressBanner } from "./TaskProgressBanner";

interface CreateCTDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  clusterId: string;
}

const selectClass =
  "flex h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-sm transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring";

type Step = "general" | "resources" | "network" | "review";
const steps: Step[] = ["general", "resources", "network", "review"];

export function CreateCTDialog({
  open,
  onOpenChange,
  clusterId,
}: CreateCTDialogProps) {
  const { data: nodes } = useClusterNodes(clusterId);
  const { data: storageList } = useClusterStorage(clusterId);
  const { data: usedVMIDs } = useClusterVMIDs(clusterId);
  const createMutation = useCreateContainer();

  const storageOptions = storageList
    ? [
        ...new Set(
          storageList
            .filter((s) => s.active && s.enabled && s.content.includes("rootdir"))
            .map((s) => s.storage),
        ),
      ].sort()
    : [];

  const nextAvailableId = useMemo(() => {
    if (!usedVMIDs || usedVMIDs.size === 0) return 100;
    let candidate = 100;
    while (usedVMIDs.has(candidate)) candidate++;
    return candidate;
  }, [usedVMIDs]);

  const [step, setStep] = useState<Step>("general");
  const [upid, setUpid] = useState<string | null>(null);

  // General
  const [vmid, setVmid] = useState("");
  const [hostname, setHostname] = useState("");
  const [node, setNode] = useState("");
  const [ostemplate, setOstemplate] = useState("");

  // Resources
  const [cores, setCores] = useState("1");
  const [memory, setMemory] = useState("512");
  const [swap, setSwap] = useState("512");
  const [diskSize, setDiskSize] = useState("8");
  const [storage, setStorage] = useState("");

  // Network
  const [bridge, setBridge] = useState("vmbr0");
  const [ipConfig, setIpConfig] = useState("dhcp");
  const [gateway, setGateway] = useState("");
  const [staticIp, setStaticIp] = useState("");
  const [unprivileged, setUnprivileged] = useState(true);
  const [startAfter, setStartAfter] = useState(false);

  useEffect(() => {
    if (open && vmid === "" && usedVMIDs) {
      setVmid(String(nextAvailableId));
    }
    if (open && node === "" && nodes && nodes.length > 0 && nodes[0]) {
      setNode(nodes[0].name);
    }
  }, [open, usedVMIDs, nextAvailableId, vmid, nodes, node]);

  const isDuplicate = usedVMIDs ? usedVMIDs.has(Number(vmid)) : false;
  const stepIdx = steps.indexOf(step);

  function buildNet0(): string {
    let net = `name=eth0,bridge=${bridge}`;
    if (ipConfig === "dhcp") {
      net += ",ip=dhcp";
    } else if (staticIp) {
      net += `,ip=${staticIp}`;
      if (gateway) {
        net += `,gw=${gateway}`;
      }
    }
    return net;
  }

  function handleSubmit() {
    const rootfs =
      storage && diskSize ? `${storage}:${diskSize}` : "";

    createMutation.mutate(
      {
        clusterId,
        body: {
          vmid: Number(vmid),
          hostname,
          node,
          ostemplate,
          storage: storage || "",
          rootfs,
          memory: Number(memory),
          swap: Number(swap),
          cores: Number(cores),
          net0: buildNet0(),
          password: "",
          ssh_keys: "",
          unprivileged,
          start: startAfter,
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
    setOstemplate("");
    setCores("1");
    setMemory("512");
    setSwap("512");
    setDiskSize("8");
    setStorage("");
    setBridge("vmbr0");
    setIpConfig("dhcp");
    setGateway("");
    setStaticIp("");
    setUnprivileged(true);
    setStartAfter(false);
    createMutation.reset();
    onOpenChange(false);
  }

  const canProceed =
    step === "general"
      ? Number(vmid) > 0 && node !== "" && ostemplate !== ""
      : step === "resources"
        ? Number(cores) > 0 && Number(memory) > 0
        : true;

  return (
    <Dialog open={open} onOpenChange={handleClose}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>Create Container</DialogTitle>
          <DialogDescription>
            Step {stepIdx + 1} of {steps.length}:{" "}
            {step.charAt(0).toUpperCase() + step.slice(1)}
          </DialogDescription>
        </DialogHeader>

        {upid ? (
          <TaskProgressBanner
            clusterId={clusterId}
            upid={upid}
            kind="ct"
            resourceId=""
            onComplete={() => {
              handleClose();
            }}
            description={`Create CT ${hostname || vmid}`}
          />
        ) : (
          <div className="space-y-4">
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
                  <Label>OS Template</Label>
                  <Input
                    value={ostemplate}
                    onChange={(e) => {
                      setOstemplate(e.target.value);
                    }}
                    placeholder="local:vztmpl/ubuntu-22.04-standard_22.04-1_amd64.tar.zst"
                  />
                  <p className="text-xs text-muted-foreground">
                    Full volume ID of the template
                  </p>
                </div>
              </div>
            )}

            {step === "resources" && (
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
                <div className="space-y-2">
                  <Label>Root Disk (GB)</Label>
                  <Input
                    type="number"
                    min={1}
                    value={diskSize}
                    onChange={(e) => {
                      setDiskSize(e.target.value);
                    }}
                  />
                </div>
                <div className="space-y-2 sm:col-span-2">
                  <Label>Storage Pool</Label>
                  <select
                    value={storage}
                    onChange={(e) => {
                      setStorage(e.target.value);
                    }}
                    className={selectClass}
                  >
                    <option value="">Default</option>
                    {storageOptions.map((s) => (
                      <option key={s} value={s}>
                        {s}
                      </option>
                    ))}
                  </select>
                </div>
              </div>
            )}

            {step === "network" && (
              <div className="grid gap-4 sm:grid-cols-2">
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
                  <Label>IP Config</Label>
                  <select
                    value={ipConfig}
                    onChange={(e) => {
                      setIpConfig(e.target.value);
                    }}
                    className={selectClass}
                  >
                    <option value="dhcp">DHCP</option>
                    <option value="static">Static</option>
                  </select>
                </div>
                {ipConfig === "static" && (
                  <>
                    <div className="space-y-2">
                      <Label>IP Address (CIDR)</Label>
                      <Input
                        value={staticIp}
                        onChange={(e) => {
                          setStaticIp(e.target.value);
                        }}
                        placeholder="192.168.1.100/24"
                      />
                    </div>
                    <div className="space-y-2">
                      <Label>Gateway</Label>
                      <Input
                        value={gateway}
                        onChange={(e) => {
                          setGateway(e.target.value);
                        }}
                        placeholder="192.168.1.1"
                      />
                    </div>
                  </>
                )}
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
                    id="ct-start"
                    checked={startAfter}
                    onCheckedChange={(checked) => {
                      setStartAfter(Boolean(checked));
                    }}
                  />
                  <Label htmlFor="ct-start" className="text-sm">
                    Start after creation
                  </Label>
                </div>
              </div>
            )}

            {step === "review" && (
              <div className="grid gap-2 text-sm">
                <div className="grid grid-cols-2 gap-x-4 gap-y-1 rounded-lg border p-3">
                  <span className="text-muted-foreground">VMID</span>
                  <span>{vmid}</span>
                  <span className="text-muted-foreground">Hostname</span>
                  <span>{hostname || "--"}</span>
                  <span className="text-muted-foreground">Node</span>
                  <span>{node}</span>
                  <span className="text-muted-foreground">Template</span>
                  <span className="truncate">{ostemplate}</span>
                  <span className="text-muted-foreground">Cores</span>
                  <span>{cores}</span>
                  <span className="text-muted-foreground">Memory</span>
                  <span>{memory} MB</span>
                  <span className="text-muted-foreground">Swap</span>
                  <span>{swap} MB</span>
                  <span className="text-muted-foreground">Root Disk</span>
                  <span>
                    {diskSize} GB{storage ? ` on ${storage}` : ""}
                  </span>
                  <span className="text-muted-foreground">Network</span>
                  <span>
                    bridge={bridge},{" "}
                    {ipConfig === "dhcp" ? "DHCP" : staticIp}
                  </span>
                  <span className="text-muted-foreground">Unprivileged</span>
                  <span>{unprivileged ? "Yes" : "No"}</span>
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
                    setStep(steps[stepIdx - 1] as Step);
                  }}
                >
                  Back
                </Button>
              )}
              {step !== "review" ? (
                <Button
                  type="button"
                  disabled={!canProceed}
                  onClick={() => {
                    setStep(steps[stepIdx + 1] as Step);
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
