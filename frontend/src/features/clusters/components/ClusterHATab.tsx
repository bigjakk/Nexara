import { useState } from "react";
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
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";
import { AlertTriangle, CheckCircle2, CircleDot, Plus, ShieldCheck, Trash2, XCircle } from "lucide-react";
import { useAuth } from "@/hooks/useAuth";
import {
  useHAResources, useHAGroups, useHAStatus, useHARules,
  useCreateHAResource, useDeleteHAResource,
  useCreateHAGroup, useDeleteHAGroup,
  useCreateHARule, useDeleteHARule,
} from "@/features/ha/api/ha-queries";
import type { HAStatusEntry } from "@/features/ha/api/ha-queries";

interface ClusterHATabProps {
  clusterId: string;
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
        // Unknown type — treat as service if it has a sid, otherwise ignore
        if (e.sid) services.push(e);
        break;
    }
  }

  return { manager, quorum, nodes, services };
}

export function ClusterHATab({ clusterId }: ClusterHATabProps) {
  const { canManage } = useAuth();
  const resourcesQuery = useHAResources(clusterId);
  const groupsQuery = useHAGroups(clusterId);
  const rulesQuery = useHARules(clusterId);
  const statusQuery = useHAStatus(clusterId);
  const createResource = useCreateHAResource(clusterId);
  const deleteResource = useDeleteHAResource(clusterId);
  const createGroup = useCreateHAGroup(clusterId);
  const deleteGroup = useDeleteHAGroup(clusterId);
  const createRule = useCreateHARule(clusterId);
  const deleteRule = useDeleteHARule(clusterId);

  const [resOpen, setResOpen] = useState(false);
  const [resSID, setResSID] = useState("");
  const [resState, setResState] = useState("started");

  const [grpOpen, setGrpOpen] = useState(false);
  const [grpName, setGrpName] = useState("");
  const [grpNodes, setGrpNodes] = useState("");

  const [ruleOpen, setRuleOpen] = useState(false);
  const [ruleName, setRuleName] = useState("");
  const [ruleType, setRuleType] = useState("node-affinity");
  const [ruleResources, setRuleResources] = useState("");
  const [ruleNodes, setRuleNodes] = useState("");

  const handleCreateResource = (e: React.SyntheticEvent) => {
    e.preventDefault();
    createResource.mutate({ sid: resSID, state: resState }, {
      onSuccess: () => { setResOpen(false); setResSID(""); },
    });
  };

  const handleCreateGroup = (e: React.SyntheticEvent) => {
    e.preventDefault();
    createGroup.mutate({ group: grpName, nodes: grpNodes }, {
      onSuccess: () => { setGrpOpen(false); setGrpName(""); setGrpNodes(""); },
    });
  };

  const handleCreateRule = (e: React.SyntheticEvent) => {
    e.preventDefault();
    createRule.mutate({ rule: ruleName, type: ruleType, resources: ruleResources, ...(ruleNodes ? { nodes: ruleNodes } : {}) }, {
      onSuccess: () => { setRuleOpen(false); setRuleName(""); setRuleResources(""); setRuleNodes(""); },
    });
  };

  // Categorize status entries by id pattern
  const { manager, quorum, nodes: nodeEntries, services: serviceEntries } = categorizeStatus(statusQuery.data ?? []);
  const statusEntries = statusQuery.data ?? [];

  // Determine if this cluster uses rules (PVE 8.3+) or groups
  const hasRules = (rulesQuery.data ?? []).length > 0;
  const hasGroups = (groupsQuery.data ?? []).length > 0;

  return (
    <Tabs defaultValue="status">
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
            {/* Manager & Quorum summary */}
            <div className="grid gap-4 md:grid-cols-3">
              <ManagerCard entry={manager} />
              <QuorumCard entry={quorum} />
              <ServiceSummaryCard services={serviceEntries} />
            </div>

            {/* Node status */}
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

            {/* Active services */}
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
                          <TableCell className="font-mono text-sm">{s.sid ?? s.id}</TableCell>
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
              <Dialog open={resOpen} onOpenChange={setResOpen}>
                <DialogTrigger asChild>
                  <Button size="sm"><Plus className="mr-2 h-4 w-4" />Add Resource</Button>
                </DialogTrigger>
                <DialogContent className="max-w-sm">
                  <DialogHeader><DialogTitle>Add HA Resource</DialogTitle></DialogHeader>
                  <form onSubmit={handleCreateResource} className="space-y-4">
                    <div className="space-y-2">
                      <Label>SID (e.g. vm:100)</Label>
                      <Input value={resSID} onChange={(e) => { setResSID(e.target.value); }} required placeholder="vm:100" />
                    </div>
                    <div className="space-y-2">
                      <Label>Requested State</Label>
                      <Input value={resState} onChange={(e) => { setResState(e.target.value); }} placeholder="started" />
                    </div>
                    <Button type="submit" disabled={createResource.isPending}>
                      {createResource.isPending ? "Creating..." : "Create"}
                    </Button>
                  </form>
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
                      <TableHead>SID</TableHead>
                      <TableHead>State</TableHead>
                      <TableHead>Status</TableHead>
                      <TableHead>Group</TableHead>
                      <TableHead>Max Relocate</TableHead>
                      {canManage("ha") && <TableHead className="text-right">Actions</TableHead>}
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {resourcesQuery.data.map((res) => (
                      <TableRow key={res.sid}>
                        <TableCell className="font-mono text-sm">{res.sid}</TableCell>
                        <TableCell>{statusBadge(res.state)}</TableCell>
                        <TableCell>{res.status ? statusBadge(res.status) : "—"}</TableCell>
                        <TableCell>{res.group || "—"}</TableCell>
                        <TableCell>{res.max_relocate}</TableCell>
                        {canManage("ha") && (
                          <TableCell className="text-right">
                            <Button variant="ghost" size="sm" onClick={() => { deleteResource.mutate(res.sid); }}>
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
      </TabsContent>

      <TabsContent value="groups" className="mt-4 space-y-4">
        {/* HA Rules section (PVE 8.3+) */}
        <Card>
          <CardHeader className="flex flex-row items-center justify-between">
            <CardTitle>HA Rules</CardTitle>
            {canManage("ha") && (
              <Dialog open={ruleOpen} onOpenChange={setRuleOpen}>
                <DialogTrigger asChild>
                  <Button size="sm"><Plus className="mr-2 h-4 w-4" />Add Rule</Button>
                </DialogTrigger>
                <DialogContent className="max-w-sm">
                  <DialogHeader><DialogTitle>Create HA Rule</DialogTitle></DialogHeader>
                  <form onSubmit={handleCreateRule} className="space-y-4">
                    <div className="space-y-2">
                      <Label>Rule Name</Label>
                      <Input value={ruleName} onChange={(e) => { setRuleName(e.target.value); }} required placeholder="my-rule" />
                    </div>
                    <div className="space-y-2">
                      <Label>Type</Label>
                      <select
                        className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm"
                        value={ruleType}
                        onChange={(e) => { setRuleType(e.target.value); }}
                      >
                        <option value="node-affinity">Node Affinity</option>
                        <option value="resource-affinity">Resource Affinity</option>
                      </select>
                    </div>
                    <div className="space-y-2">
                      <Label>Resources (e.g. vm:100,ct:101)</Label>
                      <Input value={ruleResources} onChange={(e) => { setRuleResources(e.target.value); }} required placeholder="vm:100,ct:101" />
                    </div>
                    {ruleType === "node-affinity" && (
                      <div className="space-y-2">
                        <Label>Nodes (e.g. node1:100,node2:50)</Label>
                        <Input value={ruleNodes} onChange={(e) => { setRuleNodes(e.target.value); }} placeholder="node1:100,node2:50" />
                      </div>
                    )}
                    <Button type="submit" disabled={createRule.isPending}>
                      {createRule.isPending ? "Creating..." : "Create"}
                    </Button>
                  </form>
                </DialogContent>
              </Dialog>
            )}
          </CardHeader>
          <CardContent>
            {rulesQuery.isLoading && <Skeleton className="h-20 w-full" />}
            {rulesQuery.isError && <ErrorBanner error={rulesQuery.error} />}
            {!rulesQuery.isLoading && !rulesQuery.isError && (
              (rulesQuery.data ?? []).length > 0 ? (
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>Rule</TableHead>
                      <TableHead>Type</TableHead>
                      <TableHead>Resources</TableHead>
                      <TableHead>Nodes</TableHead>
                      <TableHead>Strict</TableHead>
                      <TableHead>Status</TableHead>
                      {canManage("ha") && <TableHead className="text-right">Actions</TableHead>}
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {(rulesQuery.data ?? []).map((r) => (
                      <TableRow key={r.rule}>
                        <TableCell className="font-medium">{r.rule}</TableCell>
                        <TableCell><Badge variant="outline">{r.type}</Badge></TableCell>
                        <TableCell className="text-xs font-mono">{r.resources}</TableCell>
                        <TableCell className="text-xs font-mono">{r.nodes ?? "—"}</TableCell>
                        <TableCell>{r.strict ? <Badge variant="default">Yes</Badge> : <Badge variant="outline">No</Badge>}</TableCell>
                        <TableCell>{r.disable ? <Badge variant="secondary">Disabled</Badge> : <Badge className="bg-green-600 text-white">Enabled</Badge>}</TableCell>
                        {canManage("ha") && (
                          <TableCell className="text-right">
                            <Button variant="ghost" size="sm" onClick={() => { deleteRule.mutate(r.rule); }}>
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
                  {hasGroups ? "This cluster uses HA groups (legacy). Rules are available on PVE 8.3+." : "No HA rules configured."}
                </p>
              )
            )}
          </CardContent>
        </Card>

        {/* HA Groups section (legacy, pre-PVE 8.3) */}
        {!hasRules && (
          <Card>
            <CardHeader className="flex flex-row items-center justify-between">
              <CardTitle>HA Groups (Legacy)</CardTitle>
              {canManage("ha") && (
                <Dialog open={grpOpen} onOpenChange={setGrpOpen}>
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
                        <Label>Nodes (e.g. node1:100,node2:50)</Label>
                        <Input value={grpNodes} onChange={(e) => { setGrpNodes(e.target.value); }} required placeholder="node1:100,node2:50" />
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
              {groupsQuery.isLoading && <Skeleton className="h-20 w-full" />}
              {groupsQuery.isError && <ErrorBanner error={groupsQuery.error} />}
              {!groupsQuery.isLoading && !groupsQuery.isError && (
                (groupsQuery.data ?? []).length > 0 ? (
                  <Table>
                    <TableHeader>
                      <TableRow>
                        <TableHead>Group</TableHead>
                        <TableHead>Nodes</TableHead>
                        <TableHead>Restricted</TableHead>
                        <TableHead>No Failback</TableHead>
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
                          {canManage("ha") && (
                            <TableCell className="text-right">
                              <Button variant="ghost" size="sm" onClick={() => { deleteGroup.mutate(g.group); }}>
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
