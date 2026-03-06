import { useState } from "react";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Badge } from "@/components/ui/badge";
import { Slider } from "@/components/ui/slider";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import {
  useCreateMigration,
  useRunPreFlightCheck,
  useExecuteMigration,
  useMigrationJob,
} from "../api/migration-queries";
import type {
  CreateMigrationRequest,
  MigrationType,
  VMType,
  CheckSeverity,
} from "../types/migration";
import {
  ArrowLeftRight,
  CheckCircle2,
  XCircle,
  AlertTriangle,
  Loader2,
  Plus,
} from "lucide-react";

type WizardStep = "config" | "preflight" | "progress";

const severityIcon: Record<CheckSeverity, React.ReactNode> = {
  pass: <CheckCircle2 className="h-4 w-4 text-green-500" />,
  warn: <AlertTriangle className="h-4 w-4 text-yellow-500" />,
  fail: <XCircle className="h-4 w-4 text-red-500" />,
};

export function MigrateWizard() {
  const [open, setOpen] = useState(false);
  const [step, setStep] = useState<WizardStep>("config");
  const [jobId, setJobId] = useState("");

  // Form state.
  const [sourceClusterId, setSourceClusterId] = useState("");
  const [targetClusterId, setTargetClusterId] = useState("");
  const [sourceNode, setSourceNode] = useState("");
  const [targetNode, setTargetNode] = useState("");
  const [vmid, setVmid] = useState("");
  const [vmType, setVmType] = useState<VMType>("qemu");
  const [migrationType, setMigrationType] =
    useState<MigrationType>("intra-cluster");
  const [online, setOnline] = useState(false);
  const [bwlimit, setBwlimit] = useState([0]);
  const [deleteSource, setDeleteSource] = useState(false);
  const [targetVmid, setTargetVmid] = useState("");

  const { data: clusters } = useClusters();
  const createMutation = useCreateMigration();
  const checkMutation = useRunPreFlightCheck();
  const executeMutation = useExecuteMigration();
  const { data: job } = useMigrationJob(jobId);

  function resetForm() {
    setStep("config");
    setJobId("");
    setSourceClusterId("");
    setTargetClusterId("");
    setSourceNode("");
    setTargetNode("");
    setVmid("");
    setVmType("qemu");
    setMigrationType("intra-cluster");
    setOnline(false);
    setBwlimit([0]);
    setDeleteSource(false);
    setTargetVmid("");
  }

  function handleCreate() {
    const req: CreateMigrationRequest = {
      source_cluster_id: sourceClusterId,
      target_cluster_id:
        migrationType === "intra-cluster"
          ? sourceClusterId
          : targetClusterId,
      source_node: sourceNode,
      target_node: targetNode,
      vmid: parseInt(vmid, 10),
      vm_type: vmType,
      migration_type: migrationType,
      storage_map: {},
      network_map: {},
      online,
      bwlimit_kib: bwlimit[0] ?? 0,
      delete_source: deleteSource,
      target_vmid: targetVmid ? parseInt(targetVmid, 10) : 0,
    };

    void createMutation.mutateAsync(req).then((created) => {
      setJobId(created.id);
      void checkMutation.mutateAsync(created.id).then(() => {
        setStep("preflight");
      });
    });
  }

  function handleExecute() {
    void executeMutation.mutateAsync(jobId).then(() => {
      setStep("progress");
    });
  }

  const isFormValid =
    sourceClusterId &&
    sourceNode &&
    vmid &&
    (migrationType === "intra-cluster"
      ? targetNode.length > 0
      : targetClusterId.length > 0);

  const bwlimitValue = bwlimit[0] ?? 0;

  return (
    <Dialog
      open={open}
      onOpenChange={(v) => {
        setOpen(v);
        if (!v) resetForm();
      }}
    >
      <DialogTrigger asChild>
        <Button>
          <Plus className="mr-2 h-4 w-4" />
          New Migration
        </Button>
      </DialogTrigger>
      <DialogContent className="max-w-xl">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <ArrowLeftRight className="h-5 w-5" />
            {step === "config" && "Create Migration Job"}
            {step === "preflight" && "Pre-Flight Checks"}
            {step === "progress" && "Migration Progress"}
          </DialogTitle>
        </DialogHeader>

        {step === "config" && (
          <div className="space-y-4">
            {/* Migration Type */}
            <div className="space-y-2">
              <Label>Migration Type</Label>
              <Select
                value={migrationType}
                onValueChange={(v) => {
                  setMigrationType(v as MigrationType);
                }}
              >
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="intra-cluster">
                    Intra-Cluster (same cluster)
                  </SelectItem>
                  <SelectItem value="cross-cluster">
                    Cross-Cluster (different clusters)
                  </SelectItem>
                </SelectContent>
              </Select>
            </div>

            {/* Source Cluster */}
            <div className="space-y-2">
              <Label>Source Cluster</Label>
              <Select
                value={sourceClusterId}
                onValueChange={(v) => {
                  setSourceClusterId(v);
                }}
              >
                <SelectTrigger>
                  <SelectValue placeholder="Select source cluster" />
                </SelectTrigger>
                <SelectContent>
                  {clusters?.map((c) => (
                    <SelectItem key={c.id} value={c.id}>
                      {c.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>

            {/* Target Cluster (cross-cluster only) */}
            {migrationType === "cross-cluster" && (
              <div className="space-y-2">
                <Label>Target Cluster</Label>
                <Select
                  value={targetClusterId}
                  onValueChange={(v) => {
                    setTargetClusterId(v);
                  }}
                >
                  <SelectTrigger>
                    <SelectValue placeholder="Select target cluster" />
                  </SelectTrigger>
                  <SelectContent>
                    {clusters
                      ?.filter((c) => c.id !== sourceClusterId)
                      .map((c) => (
                        <SelectItem key={c.id} value={c.id}>
                          {c.name}
                        </SelectItem>
                      ))}
                  </SelectContent>
                </Select>
              </div>
            )}

            {/* VM Info */}
            <div className="grid grid-cols-3 gap-3">
              <div className="space-y-2">
                <Label>VMID</Label>
                <Input
                  type="number"
                  value={vmid}
                  onChange={(e) => {
                    setVmid(e.target.value);
                  }}
                  placeholder="100"
                />
              </div>
              <div className="space-y-2">
                <Label>Type</Label>
                <Select
                  value={vmType}
                  onValueChange={(v) => {
                    setVmType(v as VMType);
                  }}
                >
                  <SelectTrigger>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="qemu">QEMU VM</SelectItem>
                    <SelectItem value="lxc">LXC Container</SelectItem>
                  </SelectContent>
                </Select>
              </div>
              {migrationType === "cross-cluster" && (
                <div className="space-y-2">
                  <Label>Target VMID</Label>
                  <Input
                    type="number"
                    value={targetVmid}
                    onChange={(e) => {
                      setTargetVmid(e.target.value);
                    }}
                    placeholder="Auto"
                  />
                </div>
              )}
            </div>

            {/* Source / Target Node */}
            <div className="grid grid-cols-2 gap-3">
              <div className="space-y-2">
                <Label>Source Node</Label>
                <Input
                  value={sourceNode}
                  onChange={(e) => {
                    setSourceNode(e.target.value);
                  }}
                  placeholder="pve1"
                />
              </div>
              <div className="space-y-2">
                <Label>Target Node</Label>
                <Input
                  value={targetNode}
                  onChange={(e) => {
                    setTargetNode(e.target.value);
                  }}
                  placeholder={
                    migrationType === "cross-cluster"
                      ? "Auto (optional)"
                      : "pve2"
                  }
                />
              </div>
            </div>

            {/* Options */}
            <div className="space-y-3">
              <div className="flex items-center justify-between">
                <Label>Live Migration</Label>
                <Switch checked={online} onCheckedChange={setOnline} />
              </div>
              {migrationType === "cross-cluster" && (
                <div className="flex items-center justify-between">
                  <Label>Delete Source After Migration</Label>
                  <Switch
                    checked={deleteSource}
                    onCheckedChange={setDeleteSource}
                  />
                </div>
              )}
              <div className="space-y-2">
                <Label>
                  Bandwidth Limit:{" "}
                  {bwlimitValue === 0
                    ? "Unlimited"
                    : `${String(bwlimitValue)} KiB/s`}
                </Label>
                <Slider
                  value={bwlimit}
                  onValueChange={setBwlimit}
                  min={0}
                  max={1048576}
                  step={1024}
                />
              </div>
            </div>

            <Button
              onClick={handleCreate}
              disabled={!isFormValid || createMutation.isPending}
              className="w-full"
            >
              {createMutation.isPending || checkMutation.isPending ? (
                <>
                  <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                  Running Pre-Flight Checks...
                </>
              ) : (
                "Create & Run Pre-Flight Checks"
              )}
            </Button>
          </div>
        )}

        {step === "preflight" && job && (
          <div className="space-y-4">
            {job.check_results ? (
              <>
                <div className="space-y-2">
                  {job.check_results.checks.map((check, i) => (
                    <div
                      key={i}
                      className="flex items-start gap-2 rounded-md border p-3"
                    >
                      {severityIcon[check.severity]}
                      <div>
                        <p className="text-sm font-medium">{check.name}</p>
                        <p className="text-xs text-muted-foreground">
                          {check.message}
                        </p>
                      </div>
                    </div>
                  ))}
                </div>
                <div className="flex gap-2">
                  {job.check_results.passed ? (
                    <Button
                      onClick={handleExecute}
                      disabled={executeMutation.isPending}
                      className="flex-1"
                    >
                      {executeMutation.isPending ? (
                        <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                      ) : null}
                      Execute Migration
                    </Button>
                  ) : (
                    <p className="text-sm text-red-500">
                      Pre-flight checks failed. Fix issues and create a new job.
                    </p>
                  )}
                  <Button
                    variant="outline"
                    onClick={() => {
                      setOpen(false);
                      resetForm();
                    }}
                  >
                    Close
                  </Button>
                </div>
              </>
            ) : (
              <div className="flex items-center gap-2">
                <Loader2 className="h-4 w-4 animate-spin" />
                <p className="text-sm">Running checks...</p>
              </div>
            )}
          </div>
        )}

        {step === "progress" && job && (
          <div className="space-y-4">
            <div className="flex items-center justify-between">
              <span className="text-sm font-medium">Status</span>
              <StatusBadge status={job.status} />
            </div>
            {job.status === "migrating" && (
              <div className="space-y-2">
                <div className="h-2 overflow-hidden rounded-full bg-muted">
                  <div
                    className="h-full bg-primary transition-all"
                    style={{
                      width: `${String(Math.max(job.progress * 100, 5))}%`,
                    }}
                  />
                </div>
                <p className="text-center text-xs text-muted-foreground">
                  Migration in progress...
                </p>
              </div>
            )}
            {job.status === "completed" && (
              <p className="text-sm text-green-600">
                Migration completed successfully.
              </p>
            )}
            {job.status === "failed" && (
              <p className="text-sm text-red-500">
                Migration failed: {job.error_message}
              </p>
            )}
            <Button
              variant="outline"
              onClick={() => {
                setOpen(false);
                resetForm();
              }}
            >
              Close
            </Button>
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}

const statusColors: Record<string, string> = {
  pending: "bg-gray-100 text-gray-700",
  checking: "bg-blue-100 text-blue-700",
  migrating: "bg-yellow-100 text-yellow-700",
  completed: "bg-green-100 text-green-700",
  failed: "bg-red-100 text-red-700",
  cancelled: "bg-gray-100 text-gray-500",
};

export function StatusBadge({ status }: { status: string }) {
  return (
    <Badge variant="outline" className={statusColors[status] ?? ""}>
      {status}
    </Badge>
  );
}
