import { useMemo, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogFooter,
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
import { useCreateDRSRule, useCreateHARule } from "../api/drs-queries";
import { useSaveOutcome } from "@/hooks/useSaveOutcome";
import {
  useCluster,
  useClusterNodes,
  useClusterVMs,
} from "@/features/clusters/api/cluster-queries";
import { isPVEAtLeast, PVE_FEATURES } from "@/lib/pve-version";
import type { RuleType } from "../types/drs";
import { Plus, Search } from "lucide-react";
import { useTranslation } from "react-i18next";

type RuleTarget = "manual" | "ha";

interface CreateRuleDialogProps {
  clusterId: string;
}

export function CreateRuleDialog({ clusterId }: CreateRuleDialogProps) {
  const { t } = useTranslation("clusters");
  const [open, setOpen] = useState(false);
  const [chosenTarget, setTarget] = useState<RuleTarget>("manual");
  const [ruleType, setRuleType] = useState<RuleType>("affinity");
  const [ruleName, setRuleName] = useState("");
  const [selectedVmIds, setSelectedVmIds] = useState<number[]>([]);
  const [selectedNodes, setSelectedNodes] = useState<string[]>([]);
  const [enabled, setEnabled] = useState(true);
  const [vmSearch, setVmSearch] = useState("");
  const [nodeSearch, setNodeSearch] = useState("");

  const { data: vms } = useClusterVMs(clusterId);
  const { data: nodes } = useClusterNodes(clusterId);
  const { data: cluster } = useCluster(clusterId);
  // Proxmox HA rules arrived in PVE 9.0; on PVE 8 every create answers 501.
  // A version not known yet offers only Nexara rules, as the HA tab does.
  const haRulesSupported = isPVEAtLeast(
    cluster?.pve_version ?? "",
    PVE_FEATURES.HA_RULES,
  );
  const target: RuleTarget = haRulesSupported ? chosenTarget : "manual";

  const createManualRule = useCreateDRSRule(clusterId);
  const createHARule = useCreateHARule(clusterId);
  // The hooks opt out of the app's toast: the open dialog shows a failure, and
  // one that lands after it closed is toasted, once (useSaveOutcome).
  const settle = useSaveOutcome(open);

  const filteredVMs = useMemo(() => {
    if (!vms) return [];
    const q = vmSearch.toLowerCase();
    return vms
      .filter((vm) => !vm.template)
      .filter(
        (vm) =>
          !q ||
          vm.name.toLowerCase().includes(q) ||
          String(vm.vmid).includes(q),
      );
  }, [vms, vmSearch]);

  const filteredNodes = useMemo(() => {
    if (!nodes) return [];
    const q = nodeSearch.toLowerCase();
    return nodes.filter((n) => !q || n.name.toLowerCase().includes(q));
  }, [nodes, nodeSearch]);

  const toggleVM = (vmid: number) => {
    setSelectedVmIds((prev) =>
      prev.includes(vmid) ? prev.filter((id) => id !== vmid) : [...prev, vmid],
    );
  };

  const toggleNode = (name: string) => {
    setSelectedNodes((prev) =>
      prev.includes(name) ? prev.filter((n) => n !== name) : [...prev, name],
    );
  };

  const resetForm = () => {
    setOpen(false);
    setTarget("manual");
    setSelectedVmIds([]);
    setSelectedNodes([]);
    setRuleName("");
    setRuleType("affinity");
    setEnabled(true);
    setVmSearch("");
    setNodeSearch("");
  };

  const handleSubmit = () => {
    const clusterName = cluster?.name;
    const outcome = {
      onSuccess: resetForm,
      // Shown by the dialog, from the mutation's state.
      onError: () => undefined,
      // Put away a draft whose rule now exists, unless the dialog is open
      // again and showing it as its own.
      onLateSuccess: (_: unknown, now: { open: boolean }) => {
        if (!now.open) resetForm();
      },
    };
    if (target === "ha") {
      settle(
        createHARule.mutateAsync({
          rule_name: ruleName,
          rule_type: ruleType,
          vm_ids: selectedVmIds,
          node_names: selectedNodes,
          enabled,
        }),
        {
          action: clusterName
            ? t("drsCreateRuleDialog.creatingHARuleOn", {
                ruleName,
                clusterName,
              })
            : t("drsCreateRuleDialog.creatingHARule", { ruleName }),
          ...outcome,
        },
      );
    } else {
      settle(
        createManualRule.mutateAsync({
          rule_type: ruleType,
          vm_ids: selectedVmIds,
          node_names: selectedNodes,
          enabled,
        }),
        {
          action: clusterName
            ? t("drsCreateRuleDialog.creatingADRSRuleOn", { clusterName })
            : t("drsCreateRuleDialog.creatingADRSRule"),
          ...outcome,
        },
      );
    }
  };

  const isValid =
    (selectedVmIds.length >= 2 ||
      (ruleType === "pin" && selectedVmIds.length >= 1)) &&
    (target !== "ha" || ruleName.trim().length > 0) &&
    (ruleType !== "pin" || selectedNodes.length > 0);

  const isPending = createManualRule.isPending || createHARule.isPending;
  const activeMutation = target === "ha" ? createHARule : createManualRule;

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button size="sm">
          <Plus className="mr-2 h-4 w-4" />
          {t("drsCreateRuleDialog.addRule")}
        </Button>
      </DialogTrigger>
      <DialogContent className="max-h-[85vh] overflow-y-auto sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{t("drsCreateRuleDialog.createDRSRule")}</DialogTitle>
        </DialogHeader>

        <div className="space-y-4">
          {haRulesSupported && (
            <div className="space-y-2">
              <Label>{t("drsCreateRuleDialog.target")}</Label>
              <Select
                value={target}
                onValueChange={(v) => {
                  setTarget(v as RuleTarget);
                }}
              >
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="manual">
                    {t("drsCreateRuleDialog.nexaraOnly")}
                  </SelectItem>
                  <SelectItem value="ha">
                    {t("drsCreateRuleDialog.proxmoxHA")}
                  </SelectItem>
                </SelectContent>
              </Select>
            </div>
          )}

          {target === "ha" && (
            <div className="space-y-2">
              <Label htmlFor="rule-name">
                {t("drsCreateRuleDialog.ruleName")}
              </Label>
              <Input
                id="rule-name"
                placeholder="my-ha-rule"
                value={ruleName}
                onChange={(e) => {
                  setRuleName(e.target.value);
                }}
              />
            </div>
          )}

          <div className="space-y-2">
            <Label>{t("drsCreateRuleDialog.ruleType")}</Label>
            <Select
              value={ruleType}
              onValueChange={(v) => {
                setRuleType(v as RuleType);
              }}
            >
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="affinity">
                  {t("drsCreateRuleDialog.affinity")}
                </SelectItem>
                <SelectItem value="anti-affinity">
                  {t("drsCreateRuleDialog.antiAffinity")}
                </SelectItem>
                <SelectItem value="pin">
                  {t("drsCreateRuleDialog.pin")}
                </SelectItem>
              </SelectContent>
            </Select>
          </div>

          {/* VM Selection */}
          <div className="space-y-2">
            <Label>
              {t("drsCreateRuleDialog.vms")}{" "}
              {selectedVmIds.length > 0 && (
                <span className="text-muted-foreground">
                  {t("drsCreateRuleDialog.lengthSelected", {
                    length: selectedVmIds.length,
                  })}
                </span>
              )}
            </Label>
            <div className="relative">
              <Search className="absolute left-2 top-2.5 h-3.5 w-3.5 text-muted-foreground" />
              <Input
                placeholder={t("drsCreateRuleDialog.searchVMs")}
                className="pl-7"
                value={vmSearch}
                onChange={(e) => {
                  setVmSearch(e.target.value);
                }}
              />
            </div>
            <div className="max-h-40 overflow-y-auto rounded-md border p-1">
              {filteredVMs.length === 0 ? (
                <div className="px-2 py-3 text-center text-sm text-muted-foreground">
                  {t("drsCreateRuleDialog.noVMsFound")}
                </div>
              ) : (
                filteredVMs.map((vm) => (
                  <label
                    key={vm.vmid}
                    className="flex cursor-pointer items-center gap-2 rounded px-2 py-1.5 text-sm hover:bg-muted/50"
                  >
                    <Checkbox
                      checked={selectedVmIds.includes(vm.vmid)}
                      onCheckedChange={() => {
                        toggleVM(vm.vmid);
                      }}
                    />
                    <span className="font-mono text-xs text-muted-foreground">
                      {vm.vmid}
                    </span>
                    <span className="truncate">{vm.name}</span>
                    <span className="ml-auto text-xs text-muted-foreground">
                      {vm.status}
                    </span>
                  </label>
                ))
              )}
            </div>
          </div>

          {/* Node Selection for Pin rules */}
          {ruleType === "pin" && (
            <div className="space-y-2">
              <Label>
                {t("drsCreateRuleDialog.nodes")}{" "}
                {selectedNodes.length > 0 && (
                  <span className="text-muted-foreground">
                    {t("drsCreateRuleDialog.lengthSelected", {
                      length: selectedNodes.length,
                    })}
                  </span>
                )}
              </Label>
              <div className="relative">
                <Search className="absolute left-2 top-2.5 h-3.5 w-3.5 text-muted-foreground" />
                <Input
                  placeholder={t("drsCreateRuleDialog.searchNodes")}
                  className="pl-7"
                  value={nodeSearch}
                  onChange={(e) => {
                    setNodeSearch(e.target.value);
                  }}
                />
              </div>
              <div className="max-h-32 overflow-y-auto rounded-md border p-1">
                {filteredNodes.length === 0 ? (
                  <div className="px-2 py-3 text-center text-sm text-muted-foreground">
                    {t("drsCreateRuleDialog.noNodesFound")}
                  </div>
                ) : (
                  filteredNodes.map((node) => (
                    <label
                      key={node.name}
                      className="flex cursor-pointer items-center gap-2 rounded px-2 py-1.5 text-sm hover:bg-muted/50"
                    >
                      <Checkbox
                        checked={selectedNodes.includes(node.name)}
                        onCheckedChange={() => {
                          toggleNode(node.name);
                        }}
                      />
                      <span>{node.name}</span>
                      <span className="ml-auto text-xs text-muted-foreground">
                        {node.status}
                      </span>
                    </label>
                  ))
                )}
              </div>
            </div>
          )}

          <div className="flex items-center gap-2">
            <Checkbox
              id="rule-enabled"
              checked={enabled}
              onCheckedChange={(checked) => {
                setEnabled(checked === true);
              }}
            />
            <Label htmlFor="rule-enabled">
              {t("drsCreateRuleDialog.enabled")}
            </Label>
          </div>
        </div>

        {activeMutation.isError && (
          <p className="text-sm text-destructive">
            {activeMutation.error instanceof Error
              ? activeMutation.error.message
              : t("drsCreateRuleDialog.failedToCreateRule")}
          </p>
        )}

        <DialogFooter>
          <Button
            variant="outline"
            onClick={() => {
              setOpen(false);
            }}
          >
            {t("drsCreateRuleDialog.cancel")}
          </Button>
          <Button onClick={handleSubmit} disabled={isPending || !isValid}>
            {isPending
              ? t("drsCreateRuleDialog.creating")
              : t("drsCreateRuleDialog.createRule")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
