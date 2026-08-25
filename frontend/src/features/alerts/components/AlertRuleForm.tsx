import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Plus } from "lucide-react";
import { useCreateAlertRule } from "../api/alert-queries";
import { useClusterNodes } from "@/features/clusters/api/cluster-queries";
import { apiClient } from "@/lib/api-client";
import type { ClusterResponse, EscalationStep } from "@/types/api";
import { EscalationChainEditor } from "./EscalationChainEditor";
import { TemplateEditor } from "./TemplateEditor";

const METRICS = [
  { value: "cpu_usage", label: "CPU Usage (%)" },
  { value: "mem_percent", label: "Memory Usage (%)" },
  { value: "disk_read", label: "Disk Read (bytes/s)" },
  { value: "disk_write", label: "Disk Write (bytes/s)" },
  { value: "net_in", label: "Network In (bytes/s)" },
  { value: "net_out", label: "Network Out (bytes/s)" },
  { value: "snapshot_age_days", label: "Snapshot Age (days)" },
];

const OPERATORS = [
  { value: ">", label: ">" },
  { value: ">=", label: ">=" },
  { value: "<", label: "<" },
  { value: "<=", label: "<=" },
  { value: "==", label: "==" },
  { value: "!=", label: "!=" },
];

export function AlertRuleForm() {
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [severity, setSeverity] = useState("warning");
  const [metric, setMetric] = useState("cpu_usage");
  const [operator, setOperator] = useState(">");
  const [threshold, setThreshold] = useState("90");
  const [durationSeconds, setDurationSeconds] = useState("300");
  const [scopeType, setScopeType] = useState("cluster");
  const [clusterId, setClusterId] = useState("");
  const [nodeId, setNodeId] = useState("");
  const [vmVmid, setVmVmid] = useState("");
  const [cooldownSeconds, setCooldownSeconds] = useState("3600");
  const [escalationChain, setEscalationChain] = useState<EscalationStep[]>([]);
  const [messageTemplate, setMessageTemplate] = useState("");

  const createMutation = useCreateAlertRule();

  const { data: clusters } = useQuery({
    queryKey: ["clusters"],
    queryFn: () => apiClient.list<ClusterResponse>("/api/v1/clusters"),
  });

  const {
    data: nodes,
    isLoading: nodesLoading,
    isError: nodesError,
  } = useClusterNodes(scopeType === "node" ? clusterId : "");

  // Bounded by the backend's int32 vm_vmid: a larger number fails to bind and
  // comes back as a generic "Invalid request body" naming no field.
  const vmid = Number(vmVmid);
  const vmidValid =
    vmVmid !== "" && Number.isInteger(vmid) && vmid > 0 && vmid <= 2147483647;

  // Every scope evaluates on a binding: the engine can't run a cluster rule
  // without a cluster, a node rule without a node, or a VM rule without a
  // VMID. Submitting without one used to create a rule that never fired.
  const scopeReady =
    clusterId !== "" &&
    (scopeType !== "node" || nodeId !== "") &&
    (scopeType !== "vm" || vmidValid);

  const resetForm = () => {
    setName("");
    setDescription("");
    setSeverity("warning");
    setMetric("cpu_usage");
    setOperator(">");
    setThreshold("90");
    setDurationSeconds("300");
    setScopeType("cluster");
    setClusterId("");
    setNodeId("");
    setVmVmid("");
    setCooldownSeconds("3600");
    setEscalationChain([]);
    setMessageTemplate("");
  };

  const handleSubmit = (e: React.SyntheticEvent) => {
    e.preventDefault();
    createMutation.mutate(
      {
        name,
        description: description || undefined,
        severity: severity as "critical" | "warning" | "info",
        metric,
        operator,
        threshold: Number(threshold),
        duration_seconds: Number(durationSeconds),
        scope_type: scopeType as "cluster" | "node" | "vm",
        cluster_id: clusterId || undefined,
        node_id: scopeType === "node" ? nodeId : undefined,
        vm_vmid: scopeType === "vm" ? vmid : undefined,
        cooldown_seconds: Number(cooldownSeconds),
        escalation_chain:
          escalationChain.length > 0 ? escalationChain : undefined,
        message_template: messageTemplate || undefined,
      },
      {
        onSuccess: () => {
          setOpen(false);
          resetForm();
        },
      },
    );
  };

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button>
          <Plus className="mr-2 h-4 w-4" />
          Create Rule
        </Button>
      </DialogTrigger>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>Create Alert Rule</DialogTitle>
        </DialogHeader>
        <form onSubmit={handleSubmit} className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="name">Name</Label>
            <Input
              id="name"
              value={name}
              onChange={(e) => { setName(e.target.value); }}
              placeholder="High CPU Usage"
              required
            />
          </div>

          <div className="space-y-2">
            <Label htmlFor="description">Description</Label>
            <Input
              id="description"
              value={description}
              onChange={(e) => { setDescription(e.target.value); }}
              placeholder="Alert when CPU exceeds threshold"
            />
          </div>

          <div className="grid grid-cols-3 gap-4">
            <div className="space-y-2">
              <Label htmlFor="severity">Severity</Label>
              <Select value={severity} onValueChange={setSeverity}>
                <SelectTrigger id="severity">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="critical">Critical</SelectItem>
                  <SelectItem value="warning">Warning</SelectItem>
                  <SelectItem value="info">Info</SelectItem>
                </SelectContent>
              </Select>
            </div>

            <div className="space-y-2">
              <Label htmlFor="metric">Metric</Label>
              <Select
                value={metric}
                onValueChange={(v) => {
                  setMetric(v);
                  // snapshot_age_days reads the per-guest snapshot inventory;
                  // the backend rejects node scope for it.
                  if (v === "snapshot_age_days" && scopeType === "node") {
                    setScopeType("cluster");
                  }
                }}
              >
                <SelectTrigger id="metric">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {METRICS.map((m) => (
                    <SelectItem key={m.value} value={m.value}>
                      {m.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>

            <div className="space-y-2">
              <Label htmlFor="scope">Scope</Label>
              <Select value={scopeType} onValueChange={setScopeType}>
                <SelectTrigger id="scope">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="cluster">Cluster</SelectItem>
                  {metric !== "snapshot_age_days" && (
                    <SelectItem value="node">Node</SelectItem>
                  )}
                  <SelectItem value="vm">VM</SelectItem>
                </SelectContent>
              </Select>
            </div>
          </div>

          <div className="grid grid-cols-2 gap-4">
            <div className="space-y-2">
              <Label htmlFor="operator">Operator</Label>
              <Select value={operator} onValueChange={setOperator}>
                <SelectTrigger id="operator">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {OPERATORS.map((op) => (
                    <SelectItem key={op.value} value={op.value}>
                      {op.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>

            <div className="space-y-2">
              <Label htmlFor="threshold">Threshold</Label>
              <Input
                id="threshold"
                type="number"
                step="any"
                value={threshold}
                onChange={(e) => { setThreshold(e.target.value); }}
                required
              />
            </div>
          </div>

          <div className="space-y-2">
            <Label htmlFor="cluster">Cluster</Label>
            <Select
              value={clusterId}
              onValueChange={(v) => {
                setClusterId(v);
                // Nodes are per-cluster; a leftover pick would belong to the
                // old one, which the backend rejects.
                setNodeId("");
              }}
            >
              <SelectTrigger id="cluster">
                <SelectValue placeholder="Select cluster" />
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

          {scopeType === "node" && (
            <div className="space-y-2">
              <Label htmlFor="node">Node</Label>
              <Select
                value={nodeId}
                onValueChange={setNodeId}
                disabled={!clusterId || nodesLoading}
              >
                <SelectTrigger id="node">
                  <SelectValue
                    placeholder={
                      !clusterId
                        ? "Select a cluster first"
                        : nodesLoading
                          ? "Loading nodes..."
                          : "Select node"
                    }
                  />
                </SelectTrigger>
                <SelectContent>
                  {nodes?.map((n) => (
                    <SelectItem key={n.id} value={n.id}>
                      {n.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              {nodesError && (
                <p className="text-xs text-destructive">
                  Could not load this cluster&apos;s nodes.
                </p>
              )}
              {!nodesError && !nodesLoading && clusterId && nodes?.length === 0 && (
                <p className="text-xs text-muted-foreground">
                  This cluster has no nodes.
                </p>
              )}
            </div>
          )}

          {scopeType === "vm" && (
            <div className="space-y-2">
              <Label htmlFor="vm-vmid">VMID</Label>
              <Input
                id="vm-vmid"
                type="number"
                min="1"
                value={vmVmid}
                onChange={(e) => { setVmVmid(e.target.value); }}
                placeholder="100"
              />
              <p className="text-xs text-muted-foreground">
                Proxmox VMID of the guest to watch
              </p>
            </div>
          )}

          <div className="grid grid-cols-2 gap-4">
            <div className="space-y-2">
              <Label htmlFor="duration">Duration (seconds)</Label>
              <Input
                id="duration"
                type="number"
                value={durationSeconds}
                onChange={(e) => { setDurationSeconds(e.target.value); }}
              />
              <p className="text-xs text-muted-foreground">
                How long condition must persist
              </p>
            </div>

            <div className="space-y-2">
              <Label htmlFor="cooldown">Cooldown (seconds)</Label>
              <Input
                id="cooldown"
                type="number"
                value={cooldownSeconds}
                onChange={(e) => { setCooldownSeconds(e.target.value); }}
              />
              <p className="text-xs text-muted-foreground">
                Suppress re-fire within window
              </p>
            </div>
          </div>

          <EscalationChainEditor
            steps={escalationChain}
            onChange={setEscalationChain}
          />

          <TemplateEditor value={messageTemplate} onChange={setMessageTemplate} />

          <div className="flex justify-end gap-2 pt-2">
            <Button
              type="button"
              variant="outline"
              onClick={() => { setOpen(false); }}
            >
              Cancel
            </Button>
            <Button
              type="submit"
              disabled={createMutation.isPending || !scopeReady}
            >
              {createMutation.isPending ? "Creating..." : "Create Rule"}
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}
