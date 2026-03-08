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
  const [cooldownSeconds, setCooldownSeconds] = useState("3600");
  const [escalationChain, setEscalationChain] = useState<EscalationStep[]>([]);
  const [messageTemplate, setMessageTemplate] = useState("");

  const createMutation = useCreateAlertRule();

  const { data: clusters } = useQuery({
    queryKey: ["clusters"],
    queryFn: () => apiClient.get<ClusterResponse[]>("/api/v1/clusters"),
  });

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
              <Label>Severity</Label>
              <Select value={severity} onValueChange={setSeverity}>
                <SelectTrigger>
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
              <Label>Metric</Label>
              <Select value={metric} onValueChange={setMetric}>
                <SelectTrigger>
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
              <Label>Scope</Label>
              <Select value={scopeType} onValueChange={setScopeType}>
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="cluster">Cluster</SelectItem>
                  <SelectItem value="node">Node</SelectItem>
                  <SelectItem value="vm">VM</SelectItem>
                </SelectContent>
              </Select>
            </div>
          </div>

          <div className="grid grid-cols-2 gap-4">
            <div className="space-y-2">
              <Label>Operator</Label>
              <Select value={operator} onValueChange={setOperator}>
                <SelectTrigger>
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
            <Label>Cluster</Label>
            <Select value={clusterId} onValueChange={setClusterId}>
              <SelectTrigger>
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
            <Button type="submit" disabled={createMutation.isPending}>
              {createMutation.isPending ? "Creating..." : "Create Rule"}
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}
