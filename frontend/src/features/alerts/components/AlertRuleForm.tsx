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
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Plus } from "lucide-react";
import { useCreateAlertRule } from "../api/alert-queries";
import { useClusterNodes } from "@/features/clusters/api/cluster-queries";
import { useUpsDevices } from "@/features/power/api/ups-queries";
import { usePermissions } from "@/hooks/usePermissions";
import { apiClient } from "@/lib/api-client";
import { apiPath } from "@/lib/api-path";
import type { ClusterResponse, EscalationStep } from "@/types/api";
import { EscalationChainEditor } from "./EscalationChainEditor";
import { UPS_METRICS } from "../lib/ups-metrics";
import { FormSection } from "./FormParts";
import { TemplateEditor } from "./TemplateEditor";
import { useTranslation } from "react-i18next";
import i18n from "@/lib/i18n";

/** The line of help under a field. */
const HINT = "text-[12.5px] text-muted-foreground";

const METRICS = [
  { value: "cpu_usage", get label() { return i18n.t("alerts:alertRuleForm.cpuUsage"); } },
  { value: "mem_percent", get label() { return i18n.t("alerts:alertRuleForm.memoryUsage"); } },
  { value: "disk_read", get label() { return i18n.t("alerts:alertRuleForm.diskReadBytesS"); } },
  { value: "disk_write", get label() { return i18n.t("alerts:alertRuleForm.diskWriteBytesS"); } },
  { value: "net_in", get label() { return i18n.t("alerts:alertRuleForm.networkInBytesS"); } },
  { value: "net_out", get label() { return i18n.t("alerts:alertRuleForm.networkOutBytesS"); } },
  { value: "snapshot_age_days", get label() { return i18n.t("alerts:alertRuleForm.snapshotAgeDays"); } },
  { value: "veeam_rpo_hours", get label() { return i18n.t("alerts:alertRuleForm.veeamRecoveryPointAgeHours"); } },
  { value: "veeam_malware_status", get label() { return i18n.t("alerts:alertRuleForm.veeamMalwareVerdict03"); } },
  { value: "veeam_repo_used_percent", get label() { return i18n.t("alerts:alertRuleForm.veeamRepositoryUsage"); } },
  { value: "veeam_job_failed", get label() { return i18n.t("alerts:alertRuleForm.veeamFailedJobsCount"); } },
  { value: "pve_backup_failed", get label() { return i18n.t("alerts:alertRuleForm.proxmoxFailedBackupsCount"); } },
  { value: "pve_task_failed", get label() { return i18n.t("alerts:alertRuleForm.proxmoxFailedTasksCount"); } },
  { value: "ups_on_battery", get label() { return i18n.t("alerts:alertRuleForm.upsOnBatteryCount"); } },
  { value: "ups_battery_charge", get label() { return i18n.t("alerts:alertRuleForm.upsLowestBatteryCharge"); } },
  { value: "ups_runtime", get label() { return i18n.t("alerts:alertRuleForm.upsShortestRuntimeMinutes"); } },
  { value: "ups_load", get label() { return i18n.t("alerts:alertRuleForm.upsHighestLoad"); } },
  { value: "ups_unreachable", get label() { return i18n.t("alerts:alertRuleForm.upsUnreachableCount"); } },
  { value: "ups_replace_battery", get label() { return i18n.t("alerts:alertRuleForm.upsBatteryNeedsReplacingCount"); } },
];

/**
 * Metrics whose value is "how many happened inside the Duration window", as
 * opposed to a level sampled at a point in time.
 *
 * Worth calling out in the form because Duration means something different for
 * them. For CPU it is "sustained above the threshold for this long"; for these
 * it is the window being counted, and the default of 300s counts only the last
 * five minutes — useless for a nightly backup nobody looks at until morning.
 */
const WINDOWED_METRICS = new Set(["pve_backup_failed", "pve_task_failed"]);

/**
 * Which scopes each metric supports, mirroring notifications.MetricScopes on
 * the backend. Kept in sync deliberately rather than fetched: the backend
 * rejects an unsupported pairing anyway, and this is what stops the form from
 * offering one in the first place.
 *
 * A metric absent from this map supports the ordinary cluster/node/vm set.
 */
const METRIC_SCOPES: Record<string, string[]> = {
  snapshot_age_days: ["cluster", "vm"],
  veeam_rpo_hours: ["cluster", "vm"],
  veeam_malware_status: ["cluster", "vm"],
  // One Veeam repository holds every cluster's backups, so there is no cluster
  // to attribute its fullness to.
  veeam_repo_used_percent: ["global"],
  // A Veeam job protects many guests at once, so there is no single vm its
  // failure belongs to.
  veeam_job_failed: ["cluster"],
  // Tasks carry a node name while a rule carries a node UUID, so the backend
  // offers cluster scope only — see evaluatePVETaskFailedRule.
  pve_backup_failed: ["cluster"],
  pve_task_failed: ["cluster"],
  // A NUT server belongs to no cluster, so a UPS rule judges every UPS and
  // names the worst one, or judges the one UPS it names.
  ups_on_battery: ["global", "ups"],
  ups_battery_charge: ["global", "ups"],
  ups_runtime: ["global", "ups"],
  ups_load: ["global", "ups"],
  ups_unreachable: ["global", "ups"],
  ups_replace_battery: ["global", "ups"],
};

const ALL_SCOPES = ["cluster", "node", "vm"];

function scopesFor(metric: string): string[] {
  return METRIC_SCOPES[metric] ?? ALL_SCOPES;
}

/**
 * A sensible threshold per metric. The form's default of 90 comes from the
 * percentage metrics and is meaningless — indeed unreachable — for the others:
 * a malware verdict is 0-3, so "> 90" is a rule that is accepted, stored,
 * evaluated every tick, and can never be true. Snapping on metric change is
 * what stops that being the easiest thing to create.
 */
const METRIC_DEFAULT_THRESHOLD: Record<string, string> = {
  // Rates in bytes/s, where 90 fires on any I/O at all: 100 MiB/s.
  disk_read: "104857600",
  disk_write: "104857600",
  net_in: "104857600",
  net_out: "104857600",
  snapshot_age_days: "30",
  veeam_rpo_hours: "24",
  // With the operator ">=" below, 2 catches Suspicious and worse: the engine
  // rates a verdict Clean 0, Informative 1, Suspicious 2, Infected 3
  // (GetClusterVeeamMalwareStats). Veeam's inline encryption detection flags a
  // great many points Suspicious, so operators watching for confirmed findings
  // will want 3.
  veeam_malware_status: "2",
  veeam_repo_used_percent: "85",
  // A count, so "> 0" is the rule almost everyone wants. Left adjustable for
  // an estate with a known-flaky job that is already ticketed.
  veeam_job_failed: "0",
  pve_backup_failed: "0",
  pve_task_failed: "0",
  ups_on_battery: "0",
  ups_battery_charge: "50",
  ups_runtime: "10",
  ups_load: "80",
  ups_unreachable: "0",
  ups_replace_battery: "0",
};

/**
 * The power rules start with no cooldown. The cooldown runs from when the
 * previous alert resolved, so the form's hour would swallow the second outage
 * of a storm, when the battery is already drained.
 */
const METRIC_DEFAULT_COOLDOWN: Record<string, string> = {
  ups_on_battery: "0",
  ups_battery_charge: "0",
  ups_runtime: "0",
};

/**
 * Operator default per metric. The UPS rules that watch a level running down:
 * "> 50" on charge would fire on every healthy UPS, so the operator snaps to
 * "<" with the threshold.
 */
const METRIC_DEFAULT_OPERATOR: Record<string, string> = {
  ups_battery_charge: "<",
  ups_runtime: "<",
  // "> 2" would catch Infected alone.
  veeam_malware_status: ">=",
};

/**
 * Duration default per metric, in seconds.
 *
 * The windowed metrics get 24h rather than the form's 300s: a nightly backup
 * that failed at 02:00 has aged out of a five-minute window long before anyone
 * is awake, so the rule would evaluate false every tick and never fire. This is
 * the single most likely way to create one of these rules that silently cannot
 * work.
 */
const METRIC_DEFAULT_DURATION: Record<string, string> = {
  pve_backup_failed: "86400",
  pve_task_failed: "86400",
  // Power: seconds count. A blip of a few seconds is not an outage, and a
  // UPS that stays unreadable for two polls' worth is.
  ups_on_battery: "60",
  ups_battery_charge: "0",
  ups_runtime: "0",
  ups_unreachable: "120",
};

/**
 * Duration cap, mirroring maxDurationFor in the alerts handler.
 *
 * The windowed metrics get 30 days because a weekly backup schedule needs a
 * window longer than a week; every other metric keeps the 24-hour cap, since
 * "CPU above 90% for more than a day" is already an extreme rule.
 */
const MAX_DURATION = 86400;
const MAX_WINDOWED_DURATION = 2592000;

function maxDurationFor(metric: string): number {
  return WINDOWED_METRICS.has(metric) ? MAX_WINDOWED_DURATION : MAX_DURATION;
}

const SCOPE_LABELS: Record<string, string> = {
  get cluster() {
    return i18n.t("alerts:alertRuleForm.scopeCluster");
  },
  get node() {
    return i18n.t("alerts:alertRuleForm.scopeNode");
  },
  get vm() {
    return i18n.t("alerts:alertRuleForm.scopeVm");
  },
  get global() {
    return i18n.t("alerts:alertRuleForm.scopeGlobalVeeam");
  },
  get ups() {
    return i18n.t("alerts:alertRuleForm.scopeOneUps");
  },
};

function scopeLabel(scope: string, metric: string): string {
  if (scope === "global" && UPS_METRICS.has(metric)) return i18n.t("alerts:alertRuleForm.globalAllUPS");
  return SCOPE_LABELS[scope] ?? scope;
}

const OPERATORS = [
  { value: ">", label: ">" },
  { value: ">=", label: ">=" },
  { value: "<", label: "<" },
  { value: "<=", label: "<=" },
  { value: "==", label: "==" },
  { value: "!=", label: "!=" },
];

export function AlertRuleForm() {
  const { t } = useTranslation("alerts");
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
  const [upsId, setUpsId] = useState("");
  const [cooldownSeconds, setCooldownSeconds] = useState("3600");
  const [escalationChain, setEscalationChain] = useState<EscalationStep[]>([]);
  const [messageTemplate, setMessageTemplate] = useState("");

  const createMutation = useCreateAlertRule();
  // A rule on a UPS metric, of any scope, takes global view:ups on the server
  // (its alerts name a UPS and its reading), which the UPS list needs too:
  // without it neither the metrics nor the scope is offered.
  const { hasGlobalPermission } = usePermissions();
  const canViewUps = hasGlobalPermission("view", "ups");
  const scopes = (m: string) => scopesFor(m).filter((sc) => sc !== "ups" || canViewUps);
  const upsDevices = useUpsDevices(scopeType === "ups");

  const { data: clusters } = useQuery({
    queryKey: ["clusters"],
    queryFn: () => apiClient.list<ClusterResponse>(apiPath`/api/v1/clusters`),
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
  // Global scope binds to nothing at all — the metric describes
  // infrastructure no cluster owns — so it is ready as soon as it is chosen.
  // A UPS's rule binds to the UPS alone, filed under no cluster.
  const namesNoCluster = scopeType === "global" || scopeType === "ups";
  const scopeReady =
    (namesNoCluster || clusterId !== "") &&
    (scopeType !== "node" || nodeId !== "") &&
    (scopeType !== "vm" || vmidValid) &&
    (scopeType !== "ups" || upsId !== "");

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
    setUpsId("");
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
        scope_type: scopeType as "cluster" | "node" | "vm" | "global" | "ups",
        cluster_id: namesNoCluster ? undefined : clusterId || undefined,
        node_id: scopeType === "node" ? nodeId : undefined,
        vm_vmid: scopeType === "vm" ? vmid : undefined,
        ups_id: scopeType === "ups" ? upsId : undefined,
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
        <Button className="gap-1.5">
          <Plus className="h-4 w-4" />
          {t("alertRuleForm.createRule")}
        </Button>
      </DialogTrigger>
      <DialogContent className="max-w-xl">
        <DialogHeader>
          <DialogTitle>{t("alertRuleForm.createAlertRule")}</DialogTitle>
          <DialogDescription>
            {t("alertRuleForm.raisesAnAlertWhenA")}
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={handleSubmit} className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="name">{t("alertRuleForm.name")}</Label>
            <Input
              id="name"
              value={name}
              onChange={(e) => {
                setName(e.target.value);
              }}
              placeholder={t("alertRuleForm.highCPUUsage")}
              required
            />
          </div>

          <div className="space-y-2">
            <Label htmlFor="description">{t("alertRuleForm.description")}</Label>
            <Input
              id="description"
              value={description}
              onChange={(e) => {
                setDescription(e.target.value);
              }}
              placeholder={t("alertRuleForm.alertWhenCPUExceedsThreshold")}
            />
          </div>

          <FormSection title={t("alertRuleForm.condition")}>
            <div className="space-y-2">
              <Label htmlFor="metric">{t("alertRuleForm.metric")}</Label>
              <Select
                value={metric}
                onValueChange={(v) => {
                  setMetric(v);
                  // Inventory-backed metrics support only some scopes, and the
                  // backend rejects the rest. Snapping to a supported one
                  // beats leaving the form in a state that only fails on
                  // submit.
                  const allowed = scopes(v);
                  if (!allowed.includes(scopeType) && allowed.length > 0) {
                    setScopeType(allowed[0] ?? "cluster");
                  }
                  const preset = METRIC_DEFAULT_THRESHOLD[v];
                  if (preset !== undefined) {
                    setThreshold(preset);
                  } else if (METRIC_DEFAULT_THRESHOLD[metric] !== undefined) {
                    // Coming BACK from a specialised metric, so the threshold
                    // on screen belongs to that one. 90 is the percentage
                    // default every unbounded metric started with.
                    setThreshold("90");
                  }
                  // Same treatment for Duration, which the windowed metrics
                  // reinterpret as the window they count over. Leaving it at
                  // 300s creates a backup rule that evaluates false every tick
                  // and can never fire.
                  const durationPreset = METRIC_DEFAULT_DURATION[v];
                  if (durationPreset !== undefined) {
                    setDurationSeconds(durationPreset);
                  } else if (METRIC_DEFAULT_DURATION[metric] !== undefined) {
                    setDurationSeconds("300");
                  }
                  const operatorPreset = METRIC_DEFAULT_OPERATOR[v];
                  if (operatorPreset !== undefined) {
                    setOperator(operatorPreset);
                  } else if (METRIC_DEFAULT_OPERATOR[metric] !== undefined) {
                    setOperator(">");
                  }
                  const cooldownPreset = METRIC_DEFAULT_COOLDOWN[v];
                  if (cooldownPreset !== undefined) {
                    setCooldownSeconds(cooldownPreset);
                  } else if (METRIC_DEFAULT_COOLDOWN[metric] !== undefined) {
                    setCooldownSeconds("3600");
                  }
                }}
              >
                <SelectTrigger id="metric">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {METRICS.filter((m) => canViewUps || !UPS_METRICS.has(m.value)).map((m) => (
                    <SelectItem key={m.value} value={m.value}>
                      {m.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>

            <div className="grid grid-cols-[7rem_minmax(0,1fr)] gap-4">
              <div className="space-y-2">
                <Label htmlFor="operator">{t("alertRuleForm.operator")}</Label>
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
                <Label htmlFor="threshold">{t("alertRuleForm.threshold")}</Label>
                <Input
                  id="threshold"
                  type="number"
                  step="any"
                  value={threshold}
                  onChange={(e) => {
                    setThreshold(e.target.value);
                  }}
                  required
                />
              </div>
            </div>
          </FormSection>

          <FormSection title={t("alertRuleForm.appliesTo")}>
            <div className="grid gap-4 sm:grid-cols-2">
              <div className="space-y-2">
                <Label htmlFor="scope">{t("alertRuleForm.scope")}</Label>
                <Select value={scopeType} onValueChange={setScopeType}>
                  <SelectTrigger id="scope">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {scopes(metric).map((sc) => (
                      <SelectItem key={sc} value={sc}>
                        {scopeLabel(sc, metric)}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>

              {scopeType === "ups" && (
                <div className="space-y-2">
                  <Label htmlFor="ups">{t("alertRuleForm.ups")}</Label>
                  <Select value={upsId} onValueChange={setUpsId}>
                    <SelectTrigger id="ups">
                      <SelectValue placeholder={upsDevices.isLoading ? t("alertRuleForm.loadingUPS") : t("alertRuleForm.selectUPS")} />
                    </SelectTrigger>
                    <SelectContent>
                      {upsDevices.data?.map((d) => (
                        <SelectItem key={d.id} value={d.id}>
                          {`${d.name}@${d.server_name}`}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>
              )}

              {!namesNoCluster && (
                <div className="space-y-2">
                  <Label htmlFor="cluster">{t("alertRuleForm.cluster")}</Label>
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
                      <SelectValue placeholder={t("alertRuleForm.selectCluster")} />
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
              )}
            </div>

            {scopeType === "node" && (
              <div className="space-y-2">
                <Label htmlFor="node">{t("alertRuleForm.node")}</Label>
                <Select
                  value={nodeId}
                  onValueChange={setNodeId}
                  disabled={!clusterId || nodesLoading}
                >
                  <SelectTrigger id="node">
                    <SelectValue
                      placeholder={
                        !clusterId
                          ? t("alertRuleForm.selectAClusterFirst")
                          : nodesLoading
                            ? t("alertRuleForm.loadingNodes")
                            : t("alertRuleForm.selectNode")
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
                    {t("alertRuleForm.couldNotLoadThisCluster")}
                  </p>
                )}
                {!nodesError &&
                  !nodesLoading &&
                  clusterId &&
                  nodes?.length === 0 && (
                    <p className={HINT}>{t("alertRuleForm.thisClusterHasNoNodes")}</p>
                  )}
              </div>
            )}

            {scopeType === "vm" && (
              <div className="space-y-2">
                <Label htmlFor="vm-vmid">{t("alertRuleForm.vmid")}</Label>
                <Input
                  id="vm-vmid"
                  type="number"
                  min="1"
                  value={vmVmid}
                  onChange={(e) => {
                    setVmVmid(e.target.value);
                  }}
                  placeholder="100"
                />
                <p className={HINT}>{t("alertRuleForm.proxmoxVMIDOfTheGuest")}</p>
                {metric === "mem_percent" && (
                  <p className={HINT}>
                    {t("alertRuleForm.memoryIsJudgedOnThe")}
                  </p>
                )}
              </div>
            )}
          </FormSection>

          <FormSection title={t("alertRuleForm.timing")}>
            <div className="grid gap-4 sm:grid-cols-2">
              <div className="space-y-2">
                <Label htmlFor="duration">{t("alertRuleForm.durationSeconds")}</Label>
                <Input
                  id="duration"
                  type="number"
                  min="0"
                  max={maxDurationFor(metric)}
                  value={durationSeconds}
                  onChange={(e) => {
                    setDurationSeconds(e.target.value);
                  }}
                />
                <p className={HINT}>
                  {WINDOWED_METRICS.has(metric)
                    ? t("alertRuleForm.howFarBackToCount")
                    : t("alertRuleForm.howLongTheConditionMust")}
                </p>
              </div>

              <div className="space-y-2">
                <Label htmlFor="cooldown">{t("alertRuleForm.cooldownSeconds")}</Label>
                <Input
                  id="cooldown"
                  type="number"
                  value={cooldownSeconds}
                  onChange={(e) => {
                    setCooldownSeconds(e.target.value);
                  }}
                />
                <p className={HINT}>
                  {t("alertRuleForm.noNewAlertForThis")}
                </p>
              </div>
            </div>
          </FormSection>

          <FormSection title={t("alertRuleForm.notification")}>
            <div className="space-y-2 sm:max-w-[calc(50%-0.5rem)]">
              <Label htmlFor="severity">{t("alertRuleForm.severity")}</Label>
              <Select value={severity} onValueChange={setSeverity}>
                <SelectTrigger id="severity">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="critical">{t("alertRuleForm.critical")}</SelectItem>
                  <SelectItem value="warning">{t("alertRuleForm.warning")}</SelectItem>
                  <SelectItem value="info">{t("alertRuleForm.info")}</SelectItem>
                </SelectContent>
              </Select>
            </div>

            <EscalationChainEditor
              steps={escalationChain}
              onChange={setEscalationChain}
            />

            <TemplateEditor
              value={messageTemplate}
              onChange={setMessageTemplate}
            />
          </FormSection>

          <DialogFooter className="gap-2 sm:space-x-0">
            <Button
              type="button"
              variant="outline"
              onClick={() => {
                setOpen(false);
              }}
            >
              {t("alertRuleForm.cancel")}
            </Button>
            <Button
              type="submit"
              disabled={createMutation.isPending || !scopeReady}
            >
              {createMutation.isPending ? t("alertRuleForm.creating") : t("alertRuleForm.createRule")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
