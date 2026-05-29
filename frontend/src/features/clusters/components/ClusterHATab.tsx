import { useState, useMemo } from "react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import {
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow,
} from "@/components/ui/table";
import {
  Dialog, DialogContent, DialogHeader, DialogTitle, DialogTrigger,
} from "@/components/ui/dialog";
import {
  Select, SelectContent, SelectItem, SelectTrigger, SelectValue,
} from "@/components/ui/select";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { Switch } from "@/components/ui/switch";
import { Skeleton } from "@/components/ui/skeleton";
import {
  AlertTriangle, CheckCircle2, CircleDot, Pencil, Plus, ShieldCheck, Trash2, XCircle,
} from "lucide-react";
import { useAuth } from "@/hooks/useAuth";
import {
  useHAResources, useHAGroups, useHAStatus, useHARules, useHAManagerStatus,
  useUpdateHAResource, useDeleteHAResource,
  useCreateHAGroup, useUpdateHAGroup, useDeleteHAGroup,
  useUpdateHARule, useDeleteHARule,
  type HAStatusEntry, type HAResource, type HAGroup, type HARuleEntry,
} from "@/features/ha/api/ha-queries";
import { HAResourceForm } from "@/features/ha/components/HAResourceForm";
import { HARuleForm } from "@/features/ha/components/HARuleForm";
import { useClusterVMs, useClusterNodes } from "../api/cluster-queries";
import type { VMResponse } from "@/types/api";
import { isPVEAtLeast, PVE_FEATURES } from "@/lib/pve-version";

interface ClusterHATabProps {
  clusterId: string;
  pveVersion: string;
}

function ErrorBanner({ error }: { error: Error }) {
  const message = error.message || "Failed to load data";
  const isForbidden = message.includes("403") || message.toLowerCase().includes("permission") || message.toLowerCase().includes("forbidden");
  return (
    <div className="flex items-center gap-2 rounded-md border border-orange-300 bg-orange-50 p-3 text-sm text-orange-800 dark:border-orange-700 dark:bg-orange-950 dark:text-orange-200">
      <AlertTriangle className="h-4 w-4 flex-shrink-0" />
      <span>{isForbidden ? "Permission denied. You may need to log out and back in for new RBAC permissions to take effect." : message}</span>
    </div>
  );
}

function statusBadge(status: string) {
  switch (status) {
    case "active":
    case "started":
    case "online":
      return <Badge className="bg-green-600 text-white">{status}</Badge>;
    case "stopped":
    case "disabled":
    case "offline":
      return <Badge variant="secondary">{status}</Badge>;
    case "error":
    case "fence":
    case "recovery":
      return <Badge variant="destructive">{status}</Badge>;
    case "ignored":
      return <Badge variant="outline">{status}</Badge>;
    default:
      return <Badge variant="secondary">{status}</Badge>;
  }
}

/** Categorize HA status entries by their type field.
 *  Proxmox returns: master, quorum, lrm (per-node), service */
function categorizeStatus(entries: HAStatusEntry[]) {
  let manager: HAStatusEntry | undefined;
  let quorum: HAStatusEntry | undefined;
  const nodes: HAStatusEntry[] = [];
  const services: HAStatusEntry[] = [];

  for (const e of entries) {
    switch (e.type) {
      case "master":
        manager = e;
        break;
      case "quorum":
        quorum = e;
        break;
      case "lrm":
        nodes.push(e);
        break;
      case "service":
        services.push(e);
        break;
      default:
        if (e.sid) services.push(e);
        break;
    }
  }

  return { manager, quorum, nodes, services };
}

const HA_STATES = ["started", "stopped", "disabled", "ignored"] as const;

function vmToSID(vm: VMResponse): string {
  return `${vm.type === "lxc" ? "ct" : "vm"}:${String(vm.vmid)}`;
}

export function ClusterHATab({ clusterId, pveVersion }: ClusterHATabProps) {
  const { canManage } = useAuth();
  // PVE 9 deprecated HA groups in favor of node-affinity rules and soft-disables
  // the groups write API once migrated. Gate group creation/editing accordingly.
  const groupsDeprecated = isPVEAtLeast(pveVersion, PVE_FEATURES.HA_RULES);
  const resourcesQuery = useHAResources(clusterId);
  const groupsQuery = useHAGroups(clusterId);
  const rulesQuery = useHARules(clusterId);
  const statusQuery = useHAStatus(clusterId);
  const managerStatusQuery = useHAManagerStatus(clusterId);
  const vmsQuery = useClusterVMs(clusterId);
  const nodesQuery = useClusterNodes(clusterId);

  const updateResource = useUpdateHAResource(clusterId);
  const deleteResource = useDeleteHAResource(clusterId);
  const createGroup = useCreateHAGroup(clusterId);
  const updateGroup = useUpdateHAGroup(clusterId);
  const deleteGroup = useDeleteHAGroup(clusterId);
  const updateRule = useUpdateHARule(clusterId);
  const deleteRule = useDeleteHARule(clusterId);

  const [mutationError, setMutationError] = useState<string | null>(null);

  // --- Resource dialog state ---
  const [resCreateOpen, setResCreateOpen] = useState(false);
  const [resEditing, setResEditing] = useState<HAResource | null>(null);

  // --- Group dialog state ---
  const [grpCreateOpen, setGrpCreateOpen] = useState(false);
  const [grpName, setGrpName] = useState("");
  const [grpNodes, setGrpNodes] = useState("");
  const [grpComment, setGrpComment] = useState("");
  const [grpEditing, setGrpEditing] = useState<HAGroup | null>(null);

  // --- Rule dialog state ---
  const [ruleCreateOpen, setRuleCreateOpen] = useState(false);
  const [ruleEditing, setRuleEditing] = useState<HARuleEntry | null>(null);

  const existingHASIDs = useMemo(() => {
    const sids = new Set<string>();
    for (const r of resourcesQuery.data ?? []) sids.add(r.sid);
    return sids;
  }, [resourcesQuery.data]);

  const availableVMs = useMemo(() => {
    return (vmsQuery.data ?? []).filter((vm) => !vm.template && !existingHASIDs.has(vmToSID(vm)));
  }, [vmsQuery.data, existingHASIDs]);

  const allVMs = useMemo(() => {
    return (vmsQuery.data ?? []).filter((vm) => !vm.template);
  }, [vmsQuery.data]);
  const allNodes = nodesQuery.data ?? [];

  const vmNameBySID = useMemo(() => {
    const map = new Map<string, string>();
    for (const vm of vmsQuery.data ?? []) map.set(vmToSID(vm), vm.name);
    return map;
  }, [vmsQuery.data]);

  const handleMutationError = (err: unknown) => {
    const msg = err instanceof Error ? err.message : "Operation failed";
    setMutationError(msg);
    setTimeout(() => { setMutationError(null); }, 5000);
  };

  const handleCreateGroup = (e: React.SyntheticEvent) => {
    e.preventDefault();
    createGroup.mutate(
      {
        group: grpName,
        nodes: grpNodes,
        ...(grpComment ? { comment: grpComment } : {}),
      },
      {
        onSuccess: () => {
          setGrpCreateOpen(false);
          setGrpName("");
          setGrpNodes("");
          setGrpComment("");
        },
        onError: handleMutationError,
      },
    );
  };

  const handleUpdateGroup = (e: React.SyntheticEvent) => {
    e.preventDefault();
    if (!grpEditing) return;
    updateGroup.mutate(
      {
        group: grpEditing.group,
        nodes: grpNodes,
        comment: grpComment,
      },
      {
        onSuccess: () => { setGrpEditing(null); },
        onError: handleMutationError,
      },
    );
  };

  const startEditGroup = (g: HAGroup) => {
    setGrpEditing(g);
    setGrpNodes(g.nodes);
    setGrpComment(g.comment ?? "");
  };

  const handleQuickStateChange = (sid: string, state: string) => {
    updateResource.mutate({ sid, state }, { onError: handleMutationError });
  };

  const handleQuickRuleDisable = (rule: HARuleEntry, disabled: boolean) => {
    updateRule.mutate(
      { rule: rule.rule, type: rule.type, disable: disabled ? 1 : 0 },
      { onError: handleMutationError },
    );
  };

  const handleDeleteResource = (sid: string) => {
    deleteResource.mutate(sid, { onError: handleMutationError });
  };

  const handleDeleteGroup = (group: string) => {
    deleteGroup.mutate(group, { onError: handleMutationError });
  };

  const handleDeleteRule = (rule: string) => {
    deleteRule.mutate(rule, { onError: handleMutationError });
  };

  const { manager, quorum, nodes: nodeEntries, services: serviceEntries } = categorizeStatus(statusQuery.data ?? []);
  const statusEntries = statusQuery.data ?? [];

  const hasRules = (rulesQuery.data ?? []).length > 0;
  const rulesSupported = rulesQuery.isSuccess; // PVE 8.3+
  const hasGroups = (groupsQuery.data ?? []).length > 0;

  return (
    <Tabs defaultValue="status">
      {mutationError && (
        <div className="mb-4 flex items-center gap-2 rounded-md border border-destructive bg-destructive/10 p-3 text-sm text-destructive">
          <AlertTriangle className="h-4 w-4 flex-shrink-0" />
          <span>{mutationError}</span>
        </div>
      )}
      <TabsList>
        <TabsTrigger value="status">Status</TabsTrigger>
        <TabsTrigger value="resources">Resources</TabsTrigger>
        <TabsTrigger value="groups">Groups / Rules</TabsTrigger>
      </TabsList>

      <TabsContent value="status" className="mt-4 space-y-4">
        {statusQuery.isLoading && (
          <div className="space-y-4">
            <Skeleton className="h-24 w-full" />
            <Skeleton className="h-32 w-full" />
          </div>
        )}
        {statusQuery.isError && <ErrorBanner error={statusQuery.error} />}
        {!statusQuery.isLoading && !statusQuery.isError && (
          <>
            <div className="grid gap-4 md:grid-cols-3">
              <ManagerCard entry={manager} />
              <QuorumCard entry={quorum} />
              <ServiceSummaryCard services={serviceEntries} />
            </div>

            {nodeEntries.length > 0 && (
              <Card>
                <CardHeader><CardTitle className="text-base">Node Status</CardTitle></CardHeader>
                <CardContent>
                  <Table>
                    <TableHeader>
                      <TableRow>
                        <TableHead>Node</TableHead>
                        <TableHead>Status</TableHead>
                        <TableHead>CRM State</TableHead>
                        <TableHead>Timestamp</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {nodeEntries.map((n) => (
                        <TableRow key={n.id}>
                          <TableCell className="font-medium">{n.node ?? n.id.replace("lrm:", "")}</TableCell>
                          <TableCell>{statusBadge(n.status.includes("active") ? "active" : n.status)}</TableCell>
                          <TableCell>{n.crm_state ?? (n.status.includes("active") ? "active" : n.state ?? "—")}</TableCell>
                          <TableCell className="text-xs text-muted-foreground">
                            {n.timestamp ? new Date(n.timestamp * 1000).toLocaleString() : "—"}
                          </TableCell>
                        </TableRow>
                      ))}
                    </TableBody>
                  </Table>
                </CardContent>
              </Card>
            )}

            {serviceEntries.length > 0 && (
              <Card>
                <CardHeader><CardTitle className="text-base">Active Services</CardTitle></CardHeader>
                <CardContent>
                  <Table>
                    <TableHeader>
                      <TableRow>
                        <TableHead>Service</TableHead>
                        <TableHead>Node</TableHead>
                        <TableHead>State</TableHead>
                        <TableHead>Request State</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {serviceEntries.map((s) => (
                        <TableRow key={s.sid ?? s.id}>
                          <TableCell>
                            <span className="font-mono text-sm">{s.sid ?? s.id}</span>
                            {s.sid && vmNameBySID.get(s.sid) && (
                              <span className="ml-2 text-muted-foreground">{vmNameBySID.get(s.sid)}</span>
                            )}
                          </TableCell>
                          <TableCell>{s.node ?? "—"}</TableCell>
                          <TableCell>{statusBadge(s.state ?? s.status)}</TableCell>
                          <TableCell>{s.request_state ?? "—"}</TableCell>
                        </TableRow>
                      ))}
                    </TableBody>
                  </Table>
                </CardContent>
              </Card>
            )}

            {managerStatusQuery.data && (
              <Card>
                <CardHeader><CardTitle className="text-base">Manager Details</CardTitle></CardHeader>
                <CardContent>
                  <pre className="max-h-80 overflow-auto rounded-md bg-muted p-3 text-xs font-mono">
                    {JSON.stringify(managerStatusQuery.data, null, 2)}
                  </pre>
                </CardContent>
              </Card>
            )}

            {statusEntries.length === 0 && (
              <p className="py-8 text-center text-sm text-muted-foreground">No HA status available. HA may not be configured for this cluster.</p>
            )}
          </>
        )}
      </TabsContent>

      <TabsContent value="resources" className="mt-4">
        <Card>
          <CardHeader className="flex flex-row items-center justify-between">
            <CardTitle>HA Resources</CardTitle>
            {canManage("ha") && (
              <Dialog open={resCreateOpen} onOpenChange={setResCreateOpen}>
                <DialogTrigger asChild>
                  <Button size="sm"><Plus className="mr-2 h-4 w-4" />Add Resource</Button>
                </DialogTrigger>
                <DialogContent className="max-w-md">
                  <DialogHeader><DialogTitle>Add HA Resource</DialogTitle></DialogHeader>
                  <HAResourceForm
                    mode="create"
                    clusterId={clusterId}
                    availableVMs={availableVMs}
                    onSuccess={() => { setResCreateOpen(false); }}
                  />
                </DialogContent>
              </Dialog>
            )}
          </CardHeader>
          <CardContent>
            {resourcesQuery.isLoading && <Skeleton className="h-20 w-full" />}
            {resourcesQuery.isError && <ErrorBanner error={resourcesQuery.error} />}
            {!resourcesQuery.isLoading && !resourcesQuery.isError && (
              resourcesQuery.data && resourcesQuery.data.length > 0 ? (
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>Resource</TableHead>
                      <TableHead>State</TableHead>
                      <TableHead>Status</TableHead>
                      <TableHead>Group</TableHead>
                      <TableHead>Restart / Relocate</TableHead>
                      <TableHead>Failback</TableHead>
                      <TableHead>Comment</TableHead>
                      {canManage("ha") && <TableHead className="text-right">Actions</TableHead>}
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {resourcesQuery.data.map((res) => (
                      <TableRow key={res.sid}>
                        <TableCell>
                          <span className="font-mono text-sm">{res.sid}</span>
                          {vmNameBySID.get(res.sid) && (
                            <span className="ml-2 text-muted-foreground">{vmNameBySID.get(res.sid)}</span>
                          )}
                        </TableCell>
                        <TableCell>
                          {canManage("ha") ? (
                            <Select
                              value={res.state}
                              onValueChange={(v) => { handleQuickStateChange(res.sid, v); }}
                            >
                              <SelectTrigger className="h-7 w-28">
                                <SelectValue />
                              </SelectTrigger>
                              <SelectContent>
                                {HA_STATES.map((s) => (
                                  <SelectItem key={s} value={s}>{s}</SelectItem>
                                ))}
                              </SelectContent>
                            </Select>
                          ) : (
                            statusBadge(res.state)
                          )}
                        </TableCell>
                        <TableCell>{res.status ? statusBadge(res.status) : "—"}</TableCell>
                        <TableCell>{res.group || "—"}</TableCell>
                        <TableCell className="text-xs">
                          {res.max_restart ?? 1} / {res.max_relocate}
                        </TableCell>
                        <TableCell>
                          {res.failback === 0 ? <Badge variant="outline">Off</Badge> : <Badge variant="default">On</Badge>}
                        </TableCell>
                        <TableCell className="max-w-[12rem] truncate text-xs text-muted-foreground" title={res.comment ?? ""}>
                          {res.comment || "—"}
                        </TableCell>
                        {canManage("ha") && (
                          <TableCell className="text-right space-x-1">
                            <Button variant="ghost" size="sm" onClick={() => { setResEditing(res); }}>
                              <Pencil className="h-4 w-4" />
                            </Button>
                            <Button variant="ghost" size="sm" disabled={deleteResource.isPending} onClick={() => { handleDeleteResource(res.sid); }}>
                              <Trash2 className="h-4 w-4 text-destructive" />
                            </Button>
                          </TableCell>
                        )}
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              ) : (
                <p className="text-sm text-muted-foreground">No HA resources configured.</p>
              )
            )}
          </CardContent>
        </Card>

        {/* Edit-resource dialog */}
        <Dialog open={resEditing != null} onOpenChange={(open) => { if (!open) setResEditing(null); }}>
          <DialogContent className="max-w-md">
            <DialogHeader><DialogTitle>Edit HA Resource</DialogTitle></DialogHeader>
            {resEditing && (
              <HAResourceForm
                mode="edit"
                clusterId={clusterId}
                resource={resEditing}
                onSuccess={() => { setResEditing(null); }}
              />
            )}
          </DialogContent>
        </Dialog>
      </TabsContent>

      <TabsContent value="groups" className="mt-4 space-y-4">
        {/* HA Rules section (PVE 8.3+) */}
        <Card>
          <CardHeader className="flex flex-row items-center justify-between">
            <CardTitle>HA Rules</CardTitle>
            {canManage("ha") && rulesSupported && (
              <Dialog open={ruleCreateOpen} onOpenChange={setRuleCreateOpen}>
                <DialogTrigger asChild>
                  <Button size="sm"><Plus className="mr-2 h-4 w-4" />Add Rule</Button>
                </DialogTrigger>
                <DialogContent className="max-w-lg">
                  <DialogHeader><DialogTitle>Create HA Rule</DialogTitle></DialogHeader>
                  <HARuleForm
                    mode="create"
                    clusterId={clusterId}
                    allVMs={allVMs}
                    allNodes={allNodes}
                    onSuccess={() => { setRuleCreateOpen(false); }}
                  />
                </DialogContent>
              </Dialog>
            )}
          </CardHeader>
          <CardContent>
            {rulesQuery.isLoading && <Skeleton className="h-20 w-full" />}
            {rulesQuery.isError && <ErrorBanner error={rulesQuery.error} />}
            {!rulesQuery.isLoading && !rulesQuery.isError && (
              hasRules ? (
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>Rule</TableHead>
                      <TableHead>Type</TableHead>
                      <TableHead>Resources</TableHead>
                      <TableHead>Nodes / Affinity</TableHead>
                      <TableHead>Strict</TableHead>
                      <TableHead>Comment</TableHead>
                      <TableHead>Enabled</TableHead>
                      {canManage("ha") && <TableHead className="text-right">Actions</TableHead>}
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {(rulesQuery.data ?? []).map((r) => (
                      <TableRow key={r.rule}>
                        <TableCell className="font-medium">{r.rule}</TableCell>
                        <TableCell><Badge variant="outline">{r.type}</Badge></TableCell>
                        <TableCell className="text-xs font-mono">{r.resources}</TableCell>
                        <TableCell className="text-xs font-mono">
                          {r.type === "resource-affinity" ? (r.affinity ?? "—") : (r.nodes ?? "—")}
                        </TableCell>
                        <TableCell>
                          {r.type === "node-affinity"
                            ? (r.strict ? <Badge variant="default">Yes</Badge> : <Badge variant="outline">No</Badge>)
                            : "—"}
                        </TableCell>
                        <TableCell className="max-w-[10rem] truncate text-xs text-muted-foreground" title={r.comment ?? ""}>
                          {r.comment || "—"}
                        </TableCell>
                        <TableCell>
                          {canManage("ha") ? (
                            <Switch
                              checked={r.disable !== 1}
                              onCheckedChange={(checked) => { handleQuickRuleDisable(r, !checked); }}
                            />
                          ) : (
                            r.disable === 1 ? <Badge variant="secondary">Disabled</Badge> : <Badge className="bg-green-600 text-white">Enabled</Badge>
                          )}
                        </TableCell>
                        {canManage("ha") && (
                          <TableCell className="text-right space-x-1">
                            <Button variant="ghost" size="sm" onClick={() => { setRuleEditing(r); }}>
                              <Pencil className="h-4 w-4" />
                            </Button>
                            <Button variant="ghost" size="sm" disabled={deleteRule.isPending} onClick={() => { handleDeleteRule(r.rule); }}>
                              <Trash2 className="h-4 w-4 text-destructive" />
                            </Button>
                          </TableCell>
                        )}
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              ) : (
                <p className="text-sm text-muted-foreground">
                  {rulesSupported ? "No HA rules configured." : "HA rules require Proxmox VE 8.3 or newer."}
                </p>
              )
            )}
          </CardContent>
        </Card>

        {/* Edit-rule dialog */}
        <Dialog open={ruleEditing != null} onOpenChange={(open) => { if (!open) setRuleEditing(null); }}>
          <DialogContent className="max-w-lg">
            <DialogHeader><DialogTitle>Edit HA Rule</DialogTitle></DialogHeader>
            {ruleEditing && (
              <HARuleForm
                mode="edit"
                clusterId={clusterId}
                rule={ruleEditing}
                allVMs={allVMs}
                allNodes={allNodes}
                onSuccess={() => { setRuleEditing(null); }}
              />
            )}
          </DialogContent>
        </Dialog>

        {/* HA Groups section. On PVE 9+ Proxmox auto-migrates groups to rules
            so this list is normally empty. We still surface it for PVE 8 and
            for any cluster where groups still exist. */}
        {(hasGroups || !rulesSupported) && (
          <Card>
            <CardHeader className="flex flex-row items-center justify-between">
              <CardTitle>
                HA Groups{rulesSupported && <span className="ml-2 text-xs text-muted-foreground font-normal">(Legacy — superseded by Rules in PVE 9)</span>}
              </CardTitle>
              {canManage("ha") && !groupsDeprecated && (
                <Dialog open={grpCreateOpen} onOpenChange={setGrpCreateOpen}>
                  <DialogTrigger asChild>
                    <Button size="sm"><Plus className="mr-2 h-4 w-4" />Add Group</Button>
                  </DialogTrigger>
                  <DialogContent className="max-w-sm">
                    <DialogHeader><DialogTitle>Create HA Group</DialogTitle></DialogHeader>
                    <form onSubmit={handleCreateGroup} className="space-y-4">
                      <div className="space-y-2">
                        <Label>Group Name</Label>
                        <Input value={grpName} onChange={(e) => { setGrpName(e.target.value); }} required placeholder="mygroup" />
                      </div>
                      <div className="space-y-2">
                        <Label>Nodes</Label>
                        <Input value={grpNodes} onChange={(e) => { setGrpNodes(e.target.value); }} required placeholder="node1:100,node2:50" />
                        <p className="text-xs text-muted-foreground">Comma-separated, optional <code>:priority</code> per node.</p>
                      </div>
                      <div className="space-y-2">
                        <Label>Comment</Label>
                        <Textarea value={grpComment} rows={2} onChange={(e) => { setGrpComment(e.target.value); }} />
                      </div>
                      <Button type="submit" disabled={createGroup.isPending}>
                        {createGroup.isPending ? "Creating..." : "Create"}
                      </Button>
                    </form>
                  </DialogContent>
                </Dialog>
              )}
            </CardHeader>
            <CardContent>
              {groupsDeprecated && (
                <div className="mb-3 flex items-start gap-2 rounded-md border border-amber-300 bg-amber-50 p-3 text-xs text-amber-800 dark:border-amber-700 dark:bg-amber-950 dark:text-amber-200">
                  <AlertTriangle className="mt-0.5 h-4 w-4 flex-shrink-0" />
                  <span>
                    Proxmox VE 9 migrated HA Groups to <strong>HA Rules</strong> (node affinity).
                    Creating or editing groups is disabled &mdash; use the <strong>Rules</strong> section
                    above. Any existing groups are shown read-only.
                  </span>
                </div>
              )}
              {groupsQuery.isLoading && <Skeleton className="h-20 w-full" />}
              {groupsQuery.isError && <ErrorBanner error={groupsQuery.error} />}
              {!groupsQuery.isLoading && !groupsQuery.isError && (
                hasGroups ? (
                  <Table>
                    <TableHeader>
                      <TableRow>
                        <TableHead>Group</TableHead>
                        <TableHead>Nodes</TableHead>
                        <TableHead>Restricted</TableHead>
                        <TableHead>No Failback</TableHead>
                        <TableHead>Comment</TableHead>
                        {canManage("ha") && <TableHead className="text-right">Actions</TableHead>}
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {(groupsQuery.data ?? []).map((g) => (
                        <TableRow key={g.group}>
                          <TableCell className="font-medium">{g.group}</TableCell>
                          <TableCell className="text-xs font-mono">{g.nodes}</TableCell>
                          <TableCell>{g.restricted ? <Badge variant="default">Yes</Badge> : <Badge variant="outline">No</Badge>}</TableCell>
                          <TableCell>{g.nofailback ? <Badge variant="default">Yes</Badge> : <Badge variant="outline">No</Badge>}</TableCell>
                          <TableCell className="max-w-[10rem] truncate text-xs text-muted-foreground" title={g.comment ?? ""}>
                            {g.comment ?? "—"}
                          </TableCell>
                          {canManage("ha") && (
                            <TableCell className="text-right space-x-1">
                              {!groupsDeprecated && (
                                <Button variant="ghost" size="sm" onClick={() => { startEditGroup(g); }}>
                                  <Pencil className="h-4 w-4" />
                                </Button>
                              )}
                              <Button variant="ghost" size="sm" disabled={deleteGroup.isPending} onClick={() => { handleDeleteGroup(g.group); }}>
                                <Trash2 className="h-4 w-4 text-destructive" />
                              </Button>
                            </TableCell>
                          )}
                        </TableRow>
                      ))}
                    </TableBody>
                  </Table>
                ) : (
                  <p className="text-sm text-muted-foreground">No HA groups configured.</p>
                )
              )}
            </CardContent>
          </Card>
        )}

        {/* Edit-group dialog */}
        <Dialog open={grpEditing != null} onOpenChange={(open) => { if (!open) setGrpEditing(null); }}>
          <DialogContent className="max-w-sm">
            <DialogHeader><DialogTitle>Edit HA Group</DialogTitle></DialogHeader>
            {grpEditing && (
              <form onSubmit={handleUpdateGroup} className="space-y-4">
                <div className="space-y-2">
                  <Label>Group</Label>
                  <Input value={grpEditing.group} disabled />
                </div>
                <div className="space-y-2">
                  <Label>Nodes</Label>
                  <Input value={grpNodes} onChange={(e) => { setGrpNodes(e.target.value); }} placeholder="node1:100,node2:50" />
                </div>
                <div className="space-y-2">
                  <Label>Comment</Label>
                  <Textarea value={grpComment} rows={2} onChange={(e) => { setGrpComment(e.target.value); }} />
                </div>
                <Button type="submit" disabled={updateGroup.isPending}>
                  {updateGroup.isPending ? "Saving..." : "Save"}
                </Button>
              </form>
            )}
          </DialogContent>
        </Dialog>
      </TabsContent>
    </Tabs>
  );
}

// --- Status Summary Cards ---

function ManagerCard({ entry }: { entry: HAStatusEntry | undefined }) {
  if (!entry) {
    return (
      <Card>
        <CardHeader className="pb-2"><CardTitle className="text-sm font-medium text-muted-foreground">HA Manager</CardTitle></CardHeader>
        <CardContent>
          <div className="flex items-center gap-2">
            <XCircle className="h-5 w-5 text-muted-foreground" />
            <span className="text-sm">Not available</span>
          </div>
        </CardContent>
      </Card>
    );
  }

  const isActive = entry.status.includes("active") || entry.state === "active";
  return (
    <Card>
      <CardHeader className="pb-2"><CardTitle className="text-sm font-medium text-muted-foreground">HA Manager</CardTitle></CardHeader>
      <CardContent>
        <div className="flex items-center gap-2">
          {isActive ? <CheckCircle2 className="h-5 w-5 text-green-500" /> : <XCircle className="h-5 w-5 text-destructive" />}
          <span className="text-lg font-semibold">{isActive ? "Active" : entry.status}</span>
        </div>
        {entry.node && <p className="mt-1 text-xs text-muted-foreground">Master node: {entry.node}</p>}
      </CardContent>
    </Card>
  );
}

function QuorumCard({ entry }: { entry: HAStatusEntry | undefined }) {
  const hasQuorum = entry != null && (entry.quorum === 1 || entry.status === "OK");
  return (
    <Card>
      <CardHeader className="pb-2"><CardTitle className="text-sm font-medium text-muted-foreground">Quorum</CardTitle></CardHeader>
      <CardContent>
        <div className="flex items-center gap-2">
          {hasQuorum ? <ShieldCheck className="h-5 w-5 text-green-500" /> : <AlertTriangle className="h-5 w-5 text-yellow-500" />}
          <span className="text-lg font-semibold">{hasQuorum ? "OK" : "No Quorum"}</span>
        </div>
        {!entry && <p className="mt-1 text-xs text-muted-foreground">No quorum data</p>}
      </CardContent>
    </Card>
  );
}

function ServiceSummaryCard({ services }: { services: HAStatusEntry[] }) {
  const active = services.filter((s) => s.state === "started" || s.status === "active").length;
  return (
    <Card>
      <CardHeader className="pb-2"><CardTitle className="text-sm font-medium text-muted-foreground">HA Services</CardTitle></CardHeader>
      <CardContent>
        <div className="flex items-center gap-2">
          <CircleDot className="h-5 w-5 text-primary" />
          <span className="text-lg font-semibold">{services.length}</span>
          <span className="text-sm text-muted-foreground">total</span>
        </div>
        {services.length > 0 && (
          <p className="mt-1 text-xs text-muted-foreground">{active} active</p>
        )}
      </CardContent>
    </Card>
  );
}
