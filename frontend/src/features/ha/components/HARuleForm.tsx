import { useEffect, useMemo, useState } from "react";
import { AlertTriangle, Search } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Select, SelectContent, SelectItem, SelectTrigger, SelectValue,
} from "@/components/ui/select";
import {
  useCreateHARule,
  useCreateHAResource,
  useHAGroups,
  useHAResources,
  useUpdateHARule,
  type HARuleEntry,
} from "@/features/ha/api/ha-queries";
import { ApiClientError } from "@/lib/api-client";
import type { VMResponse } from "@/types/api";
import type { NodeResponse } from "@/types/api";

type RuleType = "node-affinity" | "resource-affinity";

/** Requested-state options offered when inline-adding a resource to HA management. */
const HA_STATES = ["started", "stopped", "disabled", "ignored"] as const;

function vmToSID(vm: VMResponse): string {
  return `${vm.type === "lxc" ? "ct" : "vm"}:${String(vm.vmid)}`;
}

/**
 * Proxmox `pve-configid` format: must start with a letter, then one or more
 * letters/digits/hyphen/underscore (so at least 2 chars) — no spaces or other
 * punctuation. Proxmox otherwise rejects the rule with
 * "invalid configuration ID '<name>'"; we mirror the rule client-side to flag
 * it before submit instead of after a round-trip.
 */
const CONFIG_ID_RE = /^[A-Za-z][A-Za-z0-9_-]+$/;

function validateRuleName(name: string): string | null {
  if (!name) return null; // empty is covered by the required attr + disabled submit
  if (!CONFIG_ID_RE.test(name)) {
    return "Use letters, numbers, hyphens, and underscores only — no spaces. Must start with a letter and be at least 2 characters.";
  }
  return null;
}

interface CommonProps {
  clusterId: string;
  allVMs: VMResponse[];
  allNodes: NodeResponse[];
  onSuccess: () => void;
}

interface CreateProps extends CommonProps {
  mode: "create";
}

interface EditProps extends CommonProps {
  mode: "edit";
  rule: HARuleEntry;
}

type Props = CreateProps | EditProps;

/** Parse "n1:100,n2:50" or "n1,n2" into a name→priority map. */
function parseNodes(s: string | undefined): Map<string, number> {
  const out = new Map<string, number>();
  if (!s) return out;
  for (const part of s.split(",")) {
    const trimmed = part.trim();
    if (!trimmed) continue;
    const [name, prio] = trimmed.split(":");
    if (!name) continue;
    const num = prio ? Number.parseInt(prio, 10) : 0;
    out.set(name, Number.isFinite(num) ? num : 0);
  }
  return out;
}

/** Serialize selected nodes back to "n1:100,n2:50" form. */
function serializeNodes(selected: Map<string, number>): string {
  return Array.from(selected.entries())
    .map(([name, prio]) => (prio > 0 ? `${name}:${String(prio)}` : name))
    .join(",");
}

/** Translate Proxmox HA rule errors into a friendlier message + hint. */
function describeHARuleError(err: unknown): { title: string; hint?: string } {
  const raw = err instanceof ApiClientError ? err.body.message : err instanceof Error ? err.message : String(err);
  const unmanaged = /cannot use unmanaged resource\(s\)\s+([^.]+)/i.exec(raw);
  if (unmanaged?.[1]) {
    return {
      title: `Some selected resources aren't HA-managed yet: ${unmanaged[1].trim()}`,
      hint: "Tick \"Add to HA management\" above, or add them on the Resources tab first.",
    };
  }
  return { title: raw || "Operation failed" };
}

export function HARuleForm(props: Props) {
  const createMut = useCreateHARule(props.clusterId);
  const updateMut = useUpdateHARule(props.clusterId);
  const createResourceMut = useCreateHAResource(props.clusterId);
  const haResourcesQuery = useHAResources(props.clusterId);
  const groupsQuery = useHAGroups(props.clusterId);

  const managedSIDs = useMemo(() => {
    const set = new Set<string>();
    for (const r of haResourcesQuery.data ?? []) set.add(r.sid);
    return set;
  }, [haResourcesQuery.data]);

  const initial = props.mode === "edit" ? props.rule : null;
  const initialType: RuleType = (initial?.type as RuleType | undefined) ?? "node-affinity";

  const [name, setName] = useState(initial?.rule ?? "");
  const [type, setType] = useState<RuleType>(initialType);
  const [selectedSIDs, setSelectedSIDs] = useState<Set<string>>(() => {
    if (!initial?.resources) return new Set();
    return new Set(initial.resources.split(",").map((s) => s.trim()).filter(Boolean));
  });
  const [nodePriorities, setNodePriorities] = useState<Map<string, number>>(() =>
    parseNodes(initial?.nodes),
  );
  const [strict, setStrict] = useState<boolean>(initial?.strict === 1);
  const [affinity, setAffinity] = useState<"positive" | "negative">(
    (initial?.affinity as "positive" | "negative" | undefined) ?? "positive",
  );
  const [comment, setComment] = useState(initial?.comment ?? "");
  const [disable, setDisable] = useState<boolean>(initial?.disable === 1);

  // Inline HA-management of selected resources Proxmox doesn't track yet.
  // When enabled, those resources are created (with the settings below) before
  // the rule itself, so Proxmox no longer rejects the rule as "unmanaged".
  const [autoManage, setAutoManage] = useState(true);
  const [resState, setResState] = useState<string>("started");
  const [resGroup, setResGroup] = useState<string>("__none__");
  const [resMaxRestart, setResMaxRestart] = useState<string>("1");
  const [resMaxRelocate, setResMaxRelocate] = useState<string>("1");
  const [resFailback, setResFailback] = useState<boolean>(true);
  const [submitError, setSubmitError] = useState<{ title: string; hint?: string } | null>(null);
  const [vmFilter, setVmFilter] = useState("");

  useEffect(() => {
    if (props.mode === "edit") {
      setName(props.rule.rule);
      setType(props.rule.type as RuleType);
      setSelectedSIDs(new Set(props.rule.resources.split(",").map((s) => s.trim()).filter(Boolean)));
      setNodePriorities(parseNodes(props.rule.nodes));
      setStrict(props.rule.strict === 1);
      setAffinity((props.rule.affinity as "positive" | "negative" | undefined) ?? "positive");
      setComment(props.rule.comment ?? "");
      setDisable(props.rule.disable === 1);
    }
  }, [props]);

  const toggleSID = (sid: string, checked: boolean) => {
    const next = new Set(selectedSIDs);
    if (checked) next.add(sid);
    else next.delete(sid);
    setSelectedSIDs(next);
  };

  const toggleNode = (nodeName: string, checked: boolean) => {
    const next = new Map(nodePriorities);
    if (checked) {
      if (!next.has(nodeName)) next.set(nodeName, 0);
    } else {
      next.delete(nodeName);
    }
    setNodePriorities(next);
  };

  const setNodePriority = (nodeName: string, priority: number) => {
    const next = new Map(nodePriorities);
    next.set(nodeName, priority);
    setNodePriorities(next);
  };

  // Validate the rule name against Proxmox's config-ID format up front (create
  // mode only — in edit mode the name is locked and already accepted).
  const nameError = useMemo(
    () => (props.mode === "create" ? validateRuleName(name) : null),
    [name, props.mode],
  );

  // Filter the resource list by SID / name / vmid for the search box.
  const filteredVMs = useMemo(() => {
    const q = vmFilter.trim().toLowerCase();
    if (!q) return props.allVMs;
    return props.allVMs.filter(
      (vm) =>
        vmToSID(vm).toLowerCase().includes(q) ||
        vm.name.toLowerCase().includes(q) ||
        String(vm.vmid).includes(q),
    );
  }, [props.allVMs, vmFilter]);

  // Pre-flight: which selected SIDs aren't yet HA-managed?
  const unmanagedSelected = useMemo(() => {
    if (!haResourcesQuery.isSuccess) return [] as string[];
    const out: string[] = [];
    for (const sid of selectedSIDs) {
      if (!managedSIDs.has(sid)) out.push(sid);
    }
    return out;
  }, [selectedSIDs, managedSIDs, haResourcesQuery.isSuccess]);

  const handleSubmit = async (e: React.SyntheticEvent) => {
    e.preventDefault();
    setSubmitError(null);
    const resources = Array.from(selectedSIDs).join(",");
    const nodesStr = serializeNodes(nodePriorities);

    try {
      // Bring any not-yet-managed selections under HA first, so Proxmox accepts
      // the rule. Sequential — concurrent writes contend on the HA config lock.
      if (autoManage && unmanagedSelected.length > 0) {
        const maxRestartNum = Number.parseInt(resMaxRestart, 10);
        const maxRelocateNum = Number.parseInt(resMaxRelocate, 10);
        const groupValue = resGroup === "__none__" ? "" : resGroup;
        for (const sid of unmanagedSelected) {
          await createResourceMut.mutateAsync({
            sid,
            state: resState,
            ...(groupValue ? { group: groupValue } : {}),
            ...(Number.isFinite(maxRestartNum) ? { max_restart: maxRestartNum } : {}),
            ...(Number.isFinite(maxRelocateNum) ? { max_relocate: maxRelocateNum } : {}),
            failback: resFailback ? 1 : 0,
          });
        }
      }

      if (props.mode === "create") {
        await createMut.mutateAsync({
          rule: name,
          type,
          resources,
          ...(type === "node-affinity" ? {
            nodes: nodesStr,
            strict: strict ? 1 : 0,
          } : {
            affinity,
          }),
          ...(comment ? { comment } : {}),
        });
      } else {
        await updateMut.mutateAsync({
          rule: props.rule.rule,
          type,
          resources,
          ...(type === "node-affinity" ? {
            nodes: nodesStr,
            strict: strict ? 1 : 0,
          } : {
            affinity,
          }),
          comment,
          disable: disable ? 1 : 0,
        });
      }
      props.onSuccess();
    } catch (err) {
      setSubmitError(describeHARuleError(err));
    }
  };

  const isPending = createResourceMut.isPending || createMut.isPending || updateMut.isPending;
  const groups = groupsQuery.data ?? [];
  const submitDisabled = isPending || !name || nameError !== null || selectedSIDs.size === 0 ||
    (type === "node-affinity" && nodePriorities.size === 0);

  return (
    <form onSubmit={(e) => void handleSubmit(e)} className="space-y-4">
      <div className="space-y-2">
        <Label>Rule Name</Label>
        <Input
          value={name}
          onChange={(e) => { setName(e.target.value); }}
          required
          disabled={props.mode === "edit"}
          placeholder="my-rule"
          aria-invalid={nameError !== null}
          className={nameError ? "border-destructive focus-visible:ring-destructive" : undefined}
        />
        {nameError && <p className="text-xs text-destructive">{nameError}</p>}
      </div>

      <div className="space-y-2">
        <Label>Type</Label>
        <Select value={type} onValueChange={(v) => { setType(v as RuleType); }} disabled={props.mode === "edit"}>
          <SelectTrigger>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="node-affinity">Node Affinity</SelectItem>
            <SelectItem value="resource-affinity">Resource Affinity</SelectItem>
          </SelectContent>
        </Select>
      </div>

      <div className="space-y-2">
        <Label>Resources</Label>
        {props.allVMs.length > 0 && (
          <div className="relative">
            <Search className="absolute left-2.5 top-2.5 h-4 w-4 text-muted-foreground" />
            <Input
              placeholder="Search by name or ID…"
              value={vmFilter}
              onChange={(e) => { setVmFilter(e.target.value); }}
              className="h-8 pl-8"
            />
          </div>
        )}
        <div className="max-h-40 overflow-y-auto rounded-md border p-2 space-y-1">
          {props.allVMs.length === 0 && (
            <p className="text-sm text-muted-foreground">No VMs/CTs found</p>
          )}
          {props.allVMs.length > 0 && filteredVMs.length === 0 && (
            <p className="text-sm text-muted-foreground">No VMs/CTs match “{vmFilter}”</p>
          )}
          {filteredVMs.map((vm) => {
            const sid = vmToSID(vm);
            const isManaged = managedSIDs.has(sid);
            return (
              <label key={vm.id} className="flex items-center gap-2 text-sm cursor-pointer hover:bg-muted/50 rounded px-1 py-0.5">
                <Checkbox
                  checked={selectedSIDs.has(sid)}
                  onCheckedChange={(c) => { toggleSID(sid, c === true); }}
                />
                <span className="font-mono text-xs">{sid}</span>
                <span className="text-muted-foreground">{vm.name}</span>
                {!isManaged && (
                  <span className="ml-auto text-[10px] uppercase tracking-wide text-amber-600 dark:text-amber-400">
                    not HA-managed
                  </span>
                )}
              </label>
            );
          })}
        </div>
        {selectedSIDs.size > 0 && (
          <p className="text-xs text-muted-foreground">{selectedSIDs.size} selected</p>
        )}
        {unmanagedSelected.length > 0 && (
          <div className="space-y-3 rounded-md border border-amber-300 bg-amber-50 p-3 dark:border-amber-800 dark:bg-amber-950/60">
            <div className="flex items-start gap-2 text-xs text-amber-900 dark:text-amber-200">
              <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
              <div>
                <span className="font-mono">{unmanagedSelected.join(", ")}</span>{" "}
                {unmanagedSelected.length === 1 ? "is" : "are"} not yet HA-managed. Proxmox won't accept
                the rule until {unmanagedSelected.length === 1 ? "it is" : "they are"} added to HA management.
              </div>
            </div>

            <label className="flex cursor-pointer items-center gap-2 text-sm text-amber-900 dark:text-amber-100">
              <Checkbox
                checked={autoManage}
                onCheckedChange={(c) => { setAutoManage(c === true); }}
              />
              <span>
                Add {unmanagedSelected.length === 1 ? "it" : "them"} to HA management with the settings below
              </span>
            </label>

            {autoManage && (
              <div className="space-y-3 rounded-md border bg-background p-3">
                <div className="grid grid-cols-2 gap-3">
                  <div className="space-y-1.5">
                    <Label className="text-xs">Requested State</Label>
                    <Select value={resState} onValueChange={setResState}>
                      <SelectTrigger className="h-8">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        {HA_STATES.map((s) => (
                          <SelectItem key={s} value={s}>{s}</SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  </div>
                  <div className="space-y-1.5">
                    <Label className="text-xs">Group</Label>
                    <Select value={resGroup} onValueChange={setResGroup}>
                      <SelectTrigger className="h-8">
                        <SelectValue placeholder="— None —" />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem value="__none__">— None —</SelectItem>
                        {groups.map((g) => (
                          <SelectItem key={g.group} value={g.group}>{g.group}</SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  </div>
                </div>

                <div className="grid grid-cols-2 gap-3">
                  <div className="space-y-1.5">
                    <Label htmlFor="auto-max-restart" className="text-xs">Max Restart</Label>
                    <Input
                      id="auto-max-restart"
                      type="number"
                      min="0"
                      max="10"
                      className="h-8"
                      value={resMaxRestart}
                      onChange={(e) => { setResMaxRestart(e.target.value); }}
                    />
                  </div>
                  <div className="space-y-1.5">
                    <Label htmlFor="auto-max-relocate" className="text-xs">Max Relocate</Label>
                    <Input
                      id="auto-max-relocate"
                      type="number"
                      min="0"
                      max="10"
                      className="h-8"
                      value={resMaxRelocate}
                      onChange={(e) => { setResMaxRelocate(e.target.value); }}
                    />
                  </div>
                </div>

                <div className="flex items-center justify-between gap-2">
                  <div>
                    <Label htmlFor="auto-failback" className="cursor-pointer text-sm">Failback</Label>
                    <p className="text-xs text-muted-foreground">Move back to a higher-priority node when available.</p>
                  </div>
                  <Switch id="auto-failback" checked={resFailback} onCheckedChange={setResFailback} />
                </div>

                <p className="text-[11px] text-muted-foreground">
                  Applied to {unmanagedSelected.length === 1 ? "this resource" : `all ${String(unmanagedSelected.length)} resources`} added here. Adjust individually later on the Resources tab.
                </p>
              </div>
            )}
          </div>
        )}
      </div>

      {type === "node-affinity" && (
        <>
          <div className="space-y-2">
            <Label>Nodes & Priorities</Label>
            <p className="text-xs text-muted-foreground">Higher priority wins. Leave at 0 for equal weight.</p>
            <div className="max-h-48 overflow-y-auto rounded-md border">
              <table className="w-full text-sm">
                <thead className="bg-muted/40">
                  <tr>
                    <th className="px-2 py-1 text-left font-medium w-10"></th>
                    <th className="px-2 py-1 text-left font-medium">Node</th>
                    <th className="px-2 py-1 text-left font-medium w-24">Priority</th>
                  </tr>
                </thead>
                <tbody>
                  {props.allNodes.map((node) => {
                    const checked = nodePriorities.has(node.name);
                    const priority = nodePriorities.get(node.name) ?? 0;
                    return (
                      <tr key={node.id} className="border-t">
                        <td className="px-2 py-1">
                          <Checkbox
                            checked={checked}
                            onCheckedChange={(c) => { toggleNode(node.name, c === true); }}
                          />
                        </td>
                        <td className="px-2 py-1">{node.name}</td>
                        <td className="px-2 py-1">
                          <Input
                            type="number"
                            min="0"
                            value={String(priority)}
                            disabled={!checked}
                            className="h-7"
                            onChange={(e) => { setNodePriority(node.name, Number.parseInt(e.target.value, 10) || 0); }}
                          />
                        </td>
                      </tr>
                    );
                  })}
                  {props.allNodes.length === 0 && (
                    <tr><td colSpan={3} className="px-2 py-2 text-muted-foreground">No nodes found.</td></tr>
                  )}
                </tbody>
              </table>
            </div>
          </div>

          <div className="flex items-center justify-between rounded-md border p-3">
            <div>
              <Label htmlFor="strict" className="cursor-pointer">Strict</Label>
              <p className="text-xs text-muted-foreground">VMs may run only on listed nodes (no failover beyond).</p>
            </div>
            <Switch id="strict" checked={strict} onCheckedChange={setStrict} />
          </div>
        </>
      )}

      {type === "resource-affinity" && (
        <div className="space-y-2">
          <Label>Affinity</Label>
          <Select value={affinity} onValueChange={(v) => { setAffinity(v as "positive" | "negative"); }}>
            <SelectTrigger>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="positive">Positive (keep together)</SelectItem>
              <SelectItem value="negative">Negative (keep apart)</SelectItem>
            </SelectContent>
          </Select>
        </div>
      )}

      <div className="space-y-2">
        <Label htmlFor="rule-comment">Comment</Label>
        <Textarea
          id="rule-comment"
          rows={2}
          value={comment}
          onChange={(e) => { setComment(e.target.value); }}
        />
      </div>

      {props.mode === "edit" && (
        <div className="flex items-center justify-between rounded-md border p-3">
          <div>
            <Label htmlFor="disable" className="cursor-pointer">Disabled</Label>
            <p className="text-xs text-muted-foreground">Rule exists but does not affect placement.</p>
          </div>
          <Switch id="disable" checked={disable} onCheckedChange={setDisable} />
        </div>
      )}

      {submitError && (
        <div className="flex items-start gap-2 rounded-md border border-destructive bg-destructive/10 p-3 text-sm text-destructive">
          <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" />
          <div>
            <div>{submitError.title}</div>
            {submitError.hint && <div className="mt-1 text-xs opacity-90">{submitError.hint}</div>}
          </div>
        </div>
      )}

      <Button type="submit" disabled={submitDisabled}>
        {isPending ? "Saving..." : props.mode === "create" ? "Create" : "Save"}
      </Button>
    </form>
  );
}
