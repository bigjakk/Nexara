import { useState, useEffect, useCallback } from "react";
import {
  Loader2,
  AlertTriangle,
  XCircle,
  CheckCircle,
  GripVertical,
  ArrowUp,
  ArrowDown,
  X,
  Bell,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { useClusterNodes } from "@/features/clusters/api/cluster-queries";
import {
  useCreateRollingUpdateJob,
  useStartRollingUpdateJob,
  usePreflightHA,
  useSSHCredentials,
} from "../api/rolling-update-queries";
import { useNotificationChannels } from "@/features/alerts/api/alert-queries";
import type { HAConflict, HAPreFlightReport } from "@/types/api";

interface CreateRollingUpdateWizardProps {
  clusterId: string;
}

function ConflictCard({ conflict }: { conflict: HAConflict }) {
  const isError = conflict.severity === "error";
  return (
    <div
      className={`flex items-start gap-2 rounded-md border p-3 text-sm ${
        isError
          ? "border-destructive/50 bg-destructive/10"
          : "border-yellow-500/50 bg-yellow-500/10"
      }`}
    >
      {isError ? (
        <XCircle className="mt-0.5 h-4 w-4 shrink-0 text-destructive" />
      ) : (
        <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-yellow-500" />
      )}
      <div>
        <p>{conflict.message}</p>
        <p className="mt-1 text-xs text-muted-foreground">
          Source: {conflict.source}
          {conflict.rule_name ? ` (${conflict.rule_name})` : ""}
        </p>
      </div>
    </div>
  );
}

export function CreateRollingUpdateWizard({
  clusterId,
}: CreateRollingUpdateWizardProps) {
  const [open, setOpen] = useState(false);
  const [step, setStep] = useState<"nodes" | "ha_review" | "configure">(
    "nodes",
  );
  const [selectedNodes, setSelectedNodes] = useState<string[]>([]);
  const [parallelism, setParallelism] = useState(1);
  const [rebootAfterUpdate, setRebootAfterUpdate] = useState(false);
  const [autoRestoreGuests, setAutoRestoreGuests] = useState(true);
  const [packageExcludes, setPackageExcludes] = useState("");
  const [haPolicy, setHaPolicy] = useState<"strict" | "warn">("warn");
  const [autoUpgrade, setAutoUpgrade] = useState(false);
  const [preflightReport, setPreflightReport] =
    useState<HAPreFlightReport | null>(null);
  const [notifyChannelId, setNotifyChannelId] = useState<string>("none");
  const [startImmediately, setStartImmediately] = useState(true);

  const { data: nodes, isLoading: nodesLoading } = useClusterNodes(clusterId);
  const { data: sshCreds } = useSSHCredentials(clusterId);
  const { data: channels } = useNotificationChannels();
  const createJob = useCreateRollingUpdateJob();
  const startJob = useStartRollingUpdateJob();
  const preflight = usePreflightHA();

  const hasSSH = sshCreds !== null && sshCreds !== undefined;

  const reset = () => {
    setStep("nodes");
    setSelectedNodes([]);
    setParallelism(1);
    setRebootAfterUpdate(true);
    setAutoRestoreGuests(true);
    setPackageExcludes("");
    setHaPolicy("warn");
    setAutoUpgrade(false);
    setPreflightReport(null);
    setNotifyChannelId("none");
    setStartImmediately(true);
  };

  // Run preflight when entering HA review step.
  useEffect(() => {
    if (step === "ha_review" && selectedNodes.length > 0) {
      preflight.mutate(
        { clusterId, nodes: selectedNodes },
        {
          onSuccess: (report) => {
            setPreflightReport(report);
          },
        },
      );
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [step]);

  const handleCreate = () => {
    const excludes = packageExcludes
      .split(",")
      .map((s) => s.trim())
      .filter(Boolean);

    createJob.mutate(
      {
        clusterId,
        nodes: selectedNodes,
        parallelism,
        reboot_after_update: rebootAfterUpdate,
        auto_restore_guests: autoRestoreGuests,
        package_excludes: excludes,
        ha_policy: haPolicy,
        auto_upgrade: autoUpgrade,
        notify_channel_id: notifyChannelId !== "none" ? notifyChannelId : undefined,
      },
      {
        onSuccess: (job) => {
          if (startImmediately) {
            startJob.mutate({ clusterId, jobId: job.id });
          }
          setOpen(false);
          reset();
        },
      },
    );
  };

  const toggleNode = (name: string) => {
    setSelectedNodes((prev) =>
      prev.includes(name)
        ? prev.filter((n) => n !== name)
        : [...prev, name],
    );
  };

  const selectAll = () => {
    if (nodes) {
      setSelectedNodes(nodes.map((n) => n.name));
    }
  };

  const moveNode = useCallback((index: number, direction: "up" | "down") => {
    setSelectedNodes((prev) => {
      const next = [...prev];
      const targetIndex = direction === "up" ? index - 1 : index + 1;
      if (targetIndex < 0 || targetIndex >= next.length) return prev;
      const temp = next[targetIndex];
      const current = next[index];
      if (temp === undefined || current === undefined) return prev;
      next[targetIndex] = current;
      next[index] = temp;
      return next;
    });
  }, []);

  const removeNode = useCallback((name: string) => {
    setSelectedNodes((prev) => prev.filter((n) => n !== name));
  }, []);

  const canProceedFromReview =
    haPolicy === "warn" || (preflightReport !== null && !preflightReport.has_errors);

  const stepTitle = {
    nodes: "Select Nodes",
    ha_review: "HA Constraint Check",
    configure: "Configure Update",
  };

  return (
    <Dialog
      open={open}
      onOpenChange={(v) => {
        setOpen(v);
        if (!v) reset();
      }}
    >
      <DialogTrigger asChild>
        <Button>New Rolling Update</Button>
      </DialogTrigger>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>{stepTitle[step]}</DialogTitle>
        </DialogHeader>

        {step === "nodes" && (
          <div className="space-y-4">
            {nodesLoading ? (
              <div className="flex justify-center py-8">
                <Loader2 className="h-6 w-6 animate-spin" />
              </div>
            ) : !nodes || nodes.length === 0 ? (
              <p className="text-sm text-muted-foreground">No nodes found</p>
            ) : (
              <>
                <div className="flex items-center justify-between">
                  <p className="text-sm text-muted-foreground">
                    {selectedNodes.length} of {nodes.length} selected
                  </p>
                  <Button variant="ghost" size="sm" onClick={selectAll}>
                    Select all
                  </Button>
                </div>
                <div className="max-h-48 space-y-2 overflow-auto">
                  {nodes.map((node) => (
                    <label
                      key={node.id}
                      className="flex cursor-pointer items-center gap-3 rounded-md border p-3 hover:bg-accent/50"
                    >
                      <Checkbox
                        checked={selectedNodes.includes(node.name)}
                        onCheckedChange={() => {
                          toggleNode(node.name);
                        }}
                      />
                      <span className="font-medium">{node.name}</span>
                      <span className="ml-auto text-xs text-muted-foreground">
                        {node.status}
                      </span>
                    </label>
                  ))}
                </div>

                {/* Upgrade order — reorderable list */}
                {selectedNodes.length > 1 && (
                  <div>
                    <p className="mb-2 text-sm font-medium">
                      Upgrade Order
                    </p>
                    <p className="mb-2 text-xs text-muted-foreground">
                      Nodes will be updated top to bottom. Use arrows to reorder.
                    </p>
                    <div className="max-h-48 space-y-1 overflow-auto">
                      {selectedNodes.map((name, i) => (
                        <div
                          key={name}
                          className="flex items-center gap-2 rounded-md border bg-card px-3 py-2"
                        >
                          <GripVertical className="h-4 w-4 shrink-0 text-muted-foreground" />
                          <span className="mr-1 text-xs font-medium text-muted-foreground">
                            {i + 1}.
                          </span>
                          <span className="flex-1 text-sm font-medium">
                            {name}
                          </span>
                          <div className="flex items-center gap-1">
                            <Button
                              variant="ghost"
                              size="icon"
                              className="h-6 w-6"
                              disabled={i === 0}
                              onClick={() => {
                                moveNode(i, "up");
                              }}
                            >
                              <ArrowUp className="h-3 w-3" />
                            </Button>
                            <Button
                              variant="ghost"
                              size="icon"
                              className="h-6 w-6"
                              disabled={i === selectedNodes.length - 1}
                              onClick={() => {
                                moveNode(i, "down");
                              }}
                            >
                              <ArrowDown className="h-3 w-3" />
                            </Button>
                            <Button
                              variant="ghost"
                              size="icon"
                              className="h-6 w-6 text-muted-foreground hover:text-destructive"
                              onClick={() => {
                                removeNode(name);
                              }}
                            >
                              <X className="h-3 w-3" />
                            </Button>
                          </div>
                        </div>
                      ))}
                    </div>
                  </div>
                )}
              </>
            )}
            <div className="flex justify-end">
              <Button
                onClick={() => {
                  setPreflightReport(null);
                  setStep("ha_review");
                }}
                disabled={selectedNodes.length === 0}
              >
                Next
              </Button>
            </div>
          </div>
        )}

        {step === "ha_review" && (
          <div className="space-y-4">
            {preflight.isPending ? (
              <div className="flex flex-col items-center gap-2 py-8">
                <Loader2 className="h-6 w-6 animate-spin" />
                <p className="text-sm text-muted-foreground">
                  Checking HA constraints...
                </p>
              </div>
            ) : preflight.isError ? (
              <div className="rounded-md border border-destructive/50 bg-destructive/10 p-4 text-sm">
                <p>Failed to check HA constraints. You can proceed with caution.</p>
              </div>
            ) : preflightReport !== null &&
              preflightReport.conflicts.length === 0 ? (
              <div className="flex items-center gap-2 rounded-md border border-green-500/50 bg-green-500/10 p-4 text-sm">
                <CheckCircle className="h-5 w-5 text-green-500" />
                <p>No HA conflicts detected. Safe to proceed.</p>
              </div>
            ) : preflightReport !== null ? (
              <div className="space-y-3">
                <p className="text-sm text-muted-foreground">
                  {preflightReport.has_errors
                    ? "Hard constraints detected that will block migrations:"
                    : "Soft constraints will be temporarily violated:"}
                </p>
                <div className="max-h-64 space-y-2 overflow-auto">
                  {preflightReport.conflicts.map((c, i) => (
                    <ConflictCard key={`${c.vmid}-${c.node}-${String(i)}`} conflict={c} />
                  ))}
                </div>
              </div>
            ) : null}

            {/* HA Policy selector */}
            {preflightReport !== null &&
              preflightReport.conflicts.length > 0 && (
                <div className="space-y-2">
                  <Label>Conflict Policy</Label>
                  <div className="flex gap-2">
                    <Button
                      variant={haPolicy === "warn" ? "default" : "outline"}
                      size="sm"
                      onClick={() => {
                        setHaPolicy("warn");
                      }}
                    >
                      Warn &amp; Proceed
                    </Button>
                    <Button
                      variant={haPolicy === "strict" ? "default" : "outline"}
                      size="sm"
                      onClick={() => {
                        setHaPolicy("strict");
                      }}
                    >
                      Strict (fail on violation)
                    </Button>
                  </div>
                  {haPolicy === "warn" && preflightReport.has_errors && (
                    <p className="text-xs text-yellow-500">
                      Hard constraints exist — migrations may fail at the
                      Proxmox level even with &quot;warn&quot; policy.
                    </p>
                  )}
                </div>
              )}

            <div className="flex justify-between">
              <Button
                variant="outline"
                onClick={() => {
                  setStep("nodes");
                }}
              >
                Back
              </Button>
              <Button
                onClick={() => {
                  setStep("configure");
                }}
                disabled={!canProceedFromReview && !preflight.isError}
              >
                Next
              </Button>
            </div>
          </div>
        )}

        {step === "configure" && (
          <div className="space-y-4">
            <div>
              <Label htmlFor="parallelism">
                Parallelism (max nodes updated at once)
              </Label>
              <Input
                id="parallelism"
                type="number"
                min={1}
                max={selectedNodes.length}
                value={parallelism}
                onChange={(e) => {
                  setParallelism(
                    Math.max(
                      1,
                      Math.min(selectedNodes.length, Number(e.target.value)),
                    ),
                  );
                }}
              />
            </div>

            <div className="flex items-center justify-between">
              <div>
                <Label htmlFor="reboot">Reboot after update</Label>
                <p className="text-xs text-muted-foreground">
                  {rebootAfterUpdate
                    ? "Always reboot after upgrade"
                    : "Auto-detect: reboot only if kernel/critical updates require it"}
                </p>
              </div>
              <Switch
                id="reboot"
                checked={rebootAfterUpdate}
                onCheckedChange={setRebootAfterUpdate}
              />
            </div>

            <div className="flex items-center justify-between">
              <Label htmlFor="restore">Auto-restore guests</Label>
              <Switch
                id="restore"
                checked={autoRestoreGuests}
                onCheckedChange={setAutoRestoreGuests}
              />
            </div>

            <div className="flex items-center justify-between">
              <div>
                <Label htmlFor="auto-upgrade">
                  Automated upgrade (SSH)
                </Label>
                <p className="text-xs text-muted-foreground">
                  {hasSSH
                    ? "Run apt dist-upgrade automatically via SSH"
                    : "Configure SSH credentials to enable"}
                </p>
              </div>
              <Switch
                id="auto-upgrade"
                checked={autoUpgrade}
                onCheckedChange={setAutoUpgrade}
                disabled={!hasSSH}
              />
            </div>

            <div>
              <Label htmlFor="excludes">
                Package excludes (comma-separated)
              </Label>
              <Input
                id="excludes"
                placeholder="e.g. pve-kernel-*, grub-*"
                value={packageExcludes}
                onChange={(e) => {
                  setPackageExcludes(e.target.value);
                }}
              />
            </div>

            <div>
              <Label htmlFor="notify-channel">
                <Bell className="mr-1 inline h-3 w-3" />
                Notify on completion/failure
              </Label>
              <Select
                value={notifyChannelId}
                onValueChange={setNotifyChannelId}
              >
                <SelectTrigger id="notify-channel" className="mt-1">
                  <SelectValue placeholder="None (no notification)" />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="none">None</SelectItem>
                  {channels?.filter((ch) => ch.enabled).map((ch) => (
                    <SelectItem key={ch.id} value={ch.id}>
                      {ch.name} ({ch.channel_type})
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>

            <div className="flex items-center justify-between">
              <div>
                <Label htmlFor="start-immediately">Start immediately</Label>
                <p className="text-xs text-muted-foreground">
                  Begin the rolling update right after creation
                </p>
              </div>
              <Switch
                id="start-immediately"
                checked={startImmediately}
                onCheckedChange={setStartImmediately}
              />
            </div>

            <p className="text-xs text-muted-foreground">
              Updating {selectedNodes.length} node
              {selectedNodes.length > 1 ? "s" : ""} in order:{" "}
              {selectedNodes.join(" → ")}
            </p>

            <div className="flex justify-between">
              <Button
                variant="outline"
                onClick={() => {
                  setStep("ha_review");
                }}
              >
                Back
              </Button>
              <Button onClick={handleCreate} disabled={createJob.isPending}>
                {createJob.isPending && (
                  <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                )}
                {startImmediately ? "Create & Start" : "Create Job"}
              </Button>
            </div>
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}
