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
import { useClusterVMIDs, useCreateVM } from "../api/vm-queries";
import { TaskProgressBanner } from "./TaskProgressBanner";

interface CreateVMDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  clusterId: string;
}

const selectClass =
  "flex h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-sm transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring";

type Step = "general" | "hardware" | "network" | "review";
const steps: Step[] = ["general", "hardware", "network", "review"];

export function CreateVMDialog({
  open,
  onOpenChange,
  clusterId,
}: CreateVMDialogProps) {
  const { data: nodes } = useClusterNodes(clusterId);
  const { data: storageList } = useClusterStorage(clusterId);
  const { data: usedVMIDs } = useClusterVMIDs(clusterId);
  const createMutation = useCreateVM();

  const storageOptions = storageList
    ? [
        ...new Set(
          storageList
            .filter((s) => s.active && s.enabled && s.content.includes("images"))
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
  const [name, setName] = useState("");
  const [node, setNode] = useState("");
  const [ostype, setOstype] = useState("l26");

  // Hardware
  const [cores, setCores] = useState("2");
  const [sockets, setSockets] = useState("1");
  const [memory, setMemory] = useState("2048");
  const [diskSize, setDiskSize] = useState("32");
  const [storage, setStorage] = useState("");

  // Network
  const [bridge, setBridge] = useState("vmbr0");
  const [netModel, setNetModel] = useState("virtio");

  // Options
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

  function handleSubmit() {
    const scsi0 =
      storage && diskSize ? `${storage}:${diskSize}` : "";

    createMutation.mutate(
      {
        clusterId,
        body: {
          vmid: Number(vmid),
          name,
          node,
          cores: Number(cores),
          sockets: Number(sockets),
          memory: Number(memory),
          scsi0,
          ide2: "",
          net0: `${netModel},bridge=${bridge}`,
          ostype,
          boot: "order=scsi0",
          cdrom: "",
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
    setName("");
    setNode("");
    setOstype("l26");
    setCores("2");
    setSockets("1");
    setMemory("2048");
    setDiskSize("32");
    setStorage("");
    setBridge("vmbr0");
    setNetModel("virtio");
    setStartAfter(false);
    createMutation.reset();
    onOpenChange(false);
  }

  const canProceed =
    step === "general"
      ? Number(vmid) > 0 && node !== ""
      : step === "hardware"
        ? Number(cores) > 0 && Number(memory) > 0
        : true;

  return (
    <Dialog open={open} onOpenChange={handleClose}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>Create Virtual Machine</DialogTitle>
          <DialogDescription>
            Step {stepIdx + 1} of {steps.length}:{" "}
            {step.charAt(0).toUpperCase() + step.slice(1)}
          </DialogDescription>
        </DialogHeader>

        {upid ? (
          <TaskProgressBanner
            clusterId={clusterId}
            upid={upid}
            kind="vm"
            resourceId=""
            onComplete={() => {
              handleClose();
            }}
            description={`Create VM ${name || vmid}`}
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
                  <Label>Name</Label>
                  <Input
                    value={name}
                    onChange={(e) => {
                      setName(e.target.value);
                    }}
                    placeholder="my-vm"
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
                  <Label>OS Type</Label>
                  <select
                    value={ostype}
                    onChange={(e) => {
                      setOstype(e.target.value);
                    }}
                    className={selectClass}
                  >
                    <option value="l26">Linux 2.6+</option>
                    <option value="l24">Linux 2.4</option>
                    <option value="win11">Windows 11</option>
                    <option value="win10">Windows 10</option>
                    <option value="win7">Windows 7</option>
                    <option value="other">Other</option>
                  </select>
                </div>
              </div>
            )}

            {step === "hardware" && (
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
                  <Label>Sockets</Label>
                  <Input
                    type="number"
                    min={1}
                    value={sockets}
                    onChange={(e) => {
                      setSockets(e.target.value);
                    }}
                  />
                </div>
                <div className="space-y-2">
                  <Label>Memory (MB)</Label>
                  <Input
                    type="number"
                    min={64}
                    step={128}
                    value={memory}
                    onChange={(e) => {
                      setMemory(e.target.value);
                    }}
                  />
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
                  <Label>Model</Label>
                  <select
                    value={netModel}
                    onChange={(e) => {
                      setNetModel(e.target.value);
                    }}
                    className={selectClass}
                  >
                    <option value="virtio">VirtIO</option>
                    <option value="e1000">Intel E1000</option>
                    <option value="rtl8139">Realtek RTL8139</option>
                  </select>
                </div>
                <div className="flex items-center gap-2 sm:col-span-2">
                  <Checkbox
                    id="start-after"
                    checked={startAfter}
                    onCheckedChange={(checked) => {
                      setStartAfter(Boolean(checked));
                    }}
                  />
                  <Label htmlFor="start-after" className="text-sm">
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
                  <span className="text-muted-foreground">Name</span>
                  <span>{name || "--"}</span>
                  <span className="text-muted-foreground">Node</span>
                  <span>{node}</span>
                  <span className="text-muted-foreground">OS Type</span>
                  <span>{ostype}</span>
                  <span className="text-muted-foreground">Cores</span>
                  <span>
                    {cores} x {sockets} socket(s)
                  </span>
                  <span className="text-muted-foreground">Memory</span>
                  <span>{memory} MB</span>
                  <span className="text-muted-foreground">Disk</span>
                  <span>
                    {diskSize} GB{storage ? ` on ${storage}` : ""}
                  </span>
                  <span className="text-muted-foreground">Network</span>
                  <span>
                    {netModel}, bridge={bridge}
                  </span>
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
                  {createMutation.isPending ? "Creating..." : "Create VM"}
                </Button>
              )}
            </DialogFooter>
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}
