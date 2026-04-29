import { useEffect, useMemo, useState } from "react";
import { AlertTriangle } from "lucide-react";
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
  useHAResources,
  useUpdateHARule,
  type HARuleEntry,
} from "@/features/ha/api/ha-queries";
import { ApiClientError } from "@/lib/api-client";
import type { VMResponse } from "@/types/api";
import type { NodeResponse } from "@/types/api";

type RuleType = "node-affinity" | "resource-affinity";

function vmToSID(vm: VMResponse): string {
  return `${vm.type === "lxc" ? "ct" : "vm"}:${String(vm.vmid)}`;
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
      hint: "Add them as HA Resources (Resources tab) before referencing them in a rule.",
    };
  }
  return { title: raw || "Operation failed" };
}

export function HARuleForm(props: Props) {
  const createMut = useCreateHARule(props.clusterId);
  const updateMut = useUpdateHARule(props.clusterId);
  const haResourcesQuery = useHAResources(props.clusterId);

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

  const handleSubmit = (e: React.SyntheticEvent) => {
    e.preventDefault();
    const resources = Array.from(selectedSIDs).join(",");
    const nodesStr = serializeNodes(nodePriorities);

    if (props.mode === "create") {
      createMut.mutate(
        {
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
        },
        { onSuccess: props.onSuccess },
      );
    } else {
      updateMut.mutate(
        {
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
        },
        { onSuccess: props.onSuccess },
      );
    }
  };

  const isPending = props.mode === "create" ? createMut.isPending : updateMut.isPending;
  const mutError = props.mode === "create" ? createMut.error : updateMut.error;
  const submitDisabled = isPending || !name || selectedSIDs.size === 0 ||
    (type === "node-affinity" && nodePriorities.size === 0);

  // Pre-flight: which selected SIDs aren't yet HA-managed?
  const unmanagedSelected = useMemo(() => {
    if (!haResourcesQuery.isSuccess) return [] as string[];
    const out: string[] = [];
    for (const sid of selectedSIDs) {
      if (!managedSIDs.has(sid)) out.push(sid);
    }
    return out;
  }, [selectedSIDs, managedSIDs, haResourcesQuery.isSuccess]);

  return (
    <form onSubmit={handleSubmit} className="space-y-4">
      <div className="space-y-2">
        <Label>Rule Name</Label>
        <Input
          value={name}
          onChange={(e) => { setName(e.target.value); }}
          required
          disabled={props.mode === "edit"}
          placeholder="my-rule"
        />
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
        <div className="max-h-40 overflow-y-auto rounded-md border p-2 space-y-1">
          {props.allVMs.length === 0 && (
            <p className="text-sm text-muted-foreground">No VMs/CTs found</p>
          )}
          {props.allVMs.map((vm) => {
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
          <div className="flex items-start gap-2 rounded-md border border-amber-300 bg-amber-50 p-2 text-xs text-amber-900 dark:border-amber-800 dark:bg-amber-950 dark:text-amber-200">
            <AlertTriangle className="mt-0.5 h-3.5 w-3.5 flex-shrink-0" />
            <div>
              <div>
                {unmanagedSelected.join(", ")} {unmanagedSelected.length === 1 ? "is" : "are"} not yet HA-managed.
              </div>
              <div className="mt-0.5 text-amber-800 dark:text-amber-300">
                Add {unmanagedSelected.length === 1 ? "it" : "them"} as HA Resources first — Proxmox will reject the rule otherwise.
              </div>
            </div>
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

      {mutError && (() => {
        const { title, hint } = describeHARuleError(mutError);
        return (
          <div className="flex items-start gap-2 rounded-md border border-destructive bg-destructive/10 p-3 text-sm text-destructive">
            <AlertTriangle className="mt-0.5 h-4 w-4 flex-shrink-0" />
            <div>
              <div>{title}</div>
              {hint && <div className="mt-1 text-xs opacity-90">{hint}</div>}
            </div>
          </div>
        );
      })()}

      <Button type="submit" disabled={submitDisabled}>
        {isPending ? "Saving..." : props.mode === "create" ? "Create" : "Save"}
      </Button>
    </form>
  );
}
