import { useState } from "react";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Label } from "@/components/ui/label";
import { ConfirmDeleteDialog } from "@/components/ConfirmDeleteDialog";
import { Plus, Trash2 } from "lucide-react";
import { FirewallRulesTable } from "@/features/networks/components/FirewallRulesTable";
import { FirewallOptionsCard } from "@/features/networks/components/FirewallOptionsCard";
import { FirewallTemplatesTable } from "@/features/networks/components/FirewallTemplatesTable";
import {
  useFirewallAliases,
  useCreateFirewallAlias,
  useDeleteFirewallAlias,
  useFirewallIPSets,
  useCreateFirewallIPSet,
  useDeleteFirewallIPSet,
  useFirewallIPSetEntries,
  useAddFirewallIPSetEntry,
  useDeleteFirewallIPSetEntry,
  useFirewallSecurityGroups,
  useCreateFirewallSecurityGroup,
  useDeleteFirewallSecurityGroup,
  useFirewallLog,
  type FirewallAlias,
  type FirewallIPSet,
  type FirewallIPSetEntry,
  type FirewallSecurityGroup,
} from "@/features/networks/api/firewall-extra-queries";
import { useClusterNodes } from "../api/cluster-queries";

interface ClusterFirewallTabProps {
  clusterId: string;
}

export function ClusterFirewallTab({ clusterId }: ClusterFirewallTabProps) {
  return (
    <Tabs defaultValue="rules">
      <TabsList>
        <TabsTrigger value="rules">Cluster Rules</TabsTrigger>
        <TabsTrigger value="templates">Templates</TabsTrigger>
        <TabsTrigger value="aliases">Aliases</TabsTrigger>
        <TabsTrigger value="ipsets">IP Sets</TabsTrigger>
        <TabsTrigger value="groups">Security Groups</TabsTrigger>
        <TabsTrigger value="log">Log</TabsTrigger>
        <TabsTrigger value="options">Options</TabsTrigger>
      </TabsList>
      <TabsContent value="rules" className="mt-4">
        <FirewallRulesTable clusterId={clusterId} />
      </TabsContent>
      <TabsContent value="templates" className="mt-4">
        <FirewallTemplatesTable clusterId={clusterId} />
      </TabsContent>
      <TabsContent value="aliases" className="mt-4">
        <FirewallAliasesSection clusterId={clusterId} />
      </TabsContent>
      <TabsContent value="ipsets" className="mt-4">
        <FirewallIPSetsSection clusterId={clusterId} />
      </TabsContent>
      <TabsContent value="groups" className="mt-4">
        <FirewallSecurityGroupsSection clusterId={clusterId} />
      </TabsContent>
      <TabsContent value="log" className="mt-4">
        <FirewallLogSection clusterId={clusterId} />
      </TabsContent>
      <TabsContent value="options" className="mt-4">
        <FirewallOptionsCard clusterId={clusterId} />
      </TabsContent>
    </Tabs>
  );
}

function FirewallAliasesSection({ clusterId }: { clusterId: string }) {
  const aliasesQuery = useFirewallAliases(clusterId);
  const createAlias = useCreateFirewallAlias(clusterId);
  const deleteAlias = useDeleteFirewallAlias(clusterId);
  const [pendingDelete, setPendingDelete] = useState<FirewallAlias | null>(
    null,
  );
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [cidr, setCidr] = useState("");
  const [comment, setComment] = useState("");

  const handleCreate = () => {
    const data: { name: string; cidr: string; comment?: string } = {
      name,
      cidr,
    };
    if (comment) data.comment = comment;
    createAlias.mutate(data, {
      onSuccess: () => {
        setOpen(false);
        setName("");
        setCidr("");
        setComment("");
      },
    });
  };

  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between space-y-0">
        <CardTitle>Firewall Aliases</CardTitle>
        <Dialog open={open} onOpenChange={setOpen}>
          <DialogTrigger asChild>
            <Button size="sm">
              <Plus className="mr-2 h-4 w-4" />
              Add Alias
            </Button>
          </DialogTrigger>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>Create Alias</DialogTitle>
            </DialogHeader>
            <div className="space-y-4">
              <div className="space-y-2">
                <Label>Name</Label>
                <Input
                  value={name}
                  onChange={(e) => {
                    setName(e.target.value);
                  }}
                />
              </div>
              <div className="space-y-2">
                <Label>CIDR</Label>
                <Input
                  value={cidr}
                  onChange={(e) => {
                    setCidr(e.target.value);
                  }}
                  placeholder="192.168.1.0/24"
                />
              </div>
              <div className="space-y-2">
                <Label>Comment</Label>
                <Input
                  value={comment}
                  onChange={(e) => {
                    setComment(e.target.value);
                  }}
                />
              </div>
            </div>
            <DialogFooter>
              <Button
                onClick={handleCreate}
                disabled={!name || !cidr || createAlias.isPending}
              >
                {createAlias.isPending ? "Creating..." : "Create"}
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      </CardHeader>
      <CardContent>
        {aliasesQuery.isLoading ? (
          <Skeleton className="h-20 w-full" />
        ) : aliasesQuery.data && aliasesQuery.data.length > 0 ? (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>CIDR</TableHead>
                <TableHead>Comment</TableHead>
                <TableHead className="text-right">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {aliasesQuery.data.map((a) => (
                <TableRow key={a.name}>
                  <TableCell className="font-medium">{a.name}</TableCell>
                  <TableCell>
                    <code className="text-xs">{a.cidr}</code>
                  </TableCell>
                  <TableCell className="text-muted-foreground">
                    {a.comment}
                  </TableCell>
                  <TableCell className="text-right">
                    <Button
                      aria-label={`Delete alias ${a.name}`}
                      variant="ghost"
                      size="sm"
                      onClick={() => {
                        setPendingDelete(a);
                      }}
                    >
                      <Trash2 className="h-4 w-4 text-destructive" />
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        ) : (
          <p className="text-sm text-muted-foreground">No aliases defined.</p>
        )}
        {/* Proxmox's remove_alias (pve-firewall
            src/PVE/API2/Firewall/Aliases.pm) deletes the alias from the
            cluster firewall config without checking whether a rule or IP set
            still names it. */}
        <ConfirmDeleteDialog
          target={pendingDelete}
          onClose={() => {
            setPendingDelete(null);
          }}
          onConfirm={(a) => {
            deleteAlias.mutate(a.name);
          }}
          title={(a) => `Delete alias ${a.name}?`}
          description={(a) =>
            `Proxmox removes the alias ${a.name} (${a.cidr}) from the cluster firewall at once, without checking whether a rule or IP set still refers to it. It cannot be undone: to get it back, create it again.`
          }
          confirmLabel="Delete Alias"
        />
      </CardContent>
    </Card>
  );
}

function FirewallIPSetsSection({ clusterId }: { clusterId: string }) {
  const setsQuery = useFirewallIPSets(clusterId);
  const createSet = useCreateFirewallIPSet(clusterId);
  const deleteSet = useDeleteFirewallIPSet(clusterId);
  const [pendingDelete, setPendingDelete] = useState<FirewallIPSet | null>(
    null,
  );
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [comment, setComment] = useState("");
  const [selectedSet, setSelectedSet] = useState("");

  const handleCreate = () => {
    const data: { name: string; comment?: string } = { name };
    if (comment) data.comment = comment;
    createSet.mutate(data, {
      onSuccess: () => {
        setOpen(false);
        setName("");
        setComment("");
      },
    });
  };

  return (
    <div className="space-y-4">
      <Card>
        <CardHeader className="flex flex-row items-center justify-between space-y-0">
          <CardTitle>IP Sets</CardTitle>
          <Dialog open={open} onOpenChange={setOpen}>
            <DialogTrigger asChild>
              <Button size="sm">
                <Plus className="mr-2 h-4 w-4" />
                Add IP Set
              </Button>
            </DialogTrigger>
            <DialogContent>
              <DialogHeader>
                <DialogTitle>Create IP Set</DialogTitle>
              </DialogHeader>
              <div className="space-y-4">
                <div className="space-y-2">
                  <Label>Name</Label>
                  <Input
                    value={name}
                    onChange={(e) => {
                      setName(e.target.value);
                    }}
                  />
                </div>
                <div className="space-y-2">
                  <Label>Comment</Label>
                  <Input
                    value={comment}
                    onChange={(e) => {
                      setComment(e.target.value);
                    }}
                  />
                </div>
              </div>
              <DialogFooter>
                <Button
                  onClick={handleCreate}
                  disabled={!name || createSet.isPending}
                >
                  {createSet.isPending ? "Creating..." : "Create"}
                </Button>
              </DialogFooter>
            </DialogContent>
          </Dialog>
        </CardHeader>
        <CardContent>
          {setsQuery.isLoading ? (
            <Skeleton className="h-20 w-full" />
          ) : setsQuery.data && setsQuery.data.length > 0 ? (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Name</TableHead>
                  <TableHead>Comment</TableHead>
                  <TableHead className="text-right">Actions</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {setsQuery.data.map((s) => (
                  <TableRow
                    key={s.name}
                    className="cursor-pointer"
                    onClick={() => {
                      setSelectedSet(selectedSet === s.name ? "" : s.name);
                    }}
                  >
                    <TableCell className="font-medium">
                      <Badge
                        variant={selectedSet === s.name ? "default" : "outline"}
                      >
                        {s.name}
                      </Badge>
                    </TableCell>
                    <TableCell className="text-muted-foreground">
                      {s.comment}
                    </TableCell>
                    <TableCell className="text-right">
                      <Button
                        aria-label={`Delete IPSet ${s.name}`}
                        variant="ghost"
                        size="sm"
                        onClick={(e) => {
                          e.stopPropagation();
                          setPendingDelete(s);
                        }}
                      >
                        <Trash2 className="h-4 w-4 text-destructive" />
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          ) : (
            <p className="text-sm text-muted-foreground">No IP sets defined.</p>
          )}
        </CardContent>
      </Card>
      {/* Proxmox's delete_ipset (pve-firewall src/PVE/API2/Firewall/IPSet.pm)
          dies "IPSet '<name>' is not empty" unless force is set, and
          proxmox.Client DeleteFirewallIPSet never sends it. It checks nothing
          else — not whether a rule still refers to the set. Rendered outside
          the table: a portal's clicks still bubble through the React tree,
          and the row's onClick would toggle the selected set. */}
      <ConfirmDeleteDialog
        target={pendingDelete}
        onClose={() => {
          setPendingDelete(null);
        }}
        onConfirm={(s) => {
          deleteSet.mutate(s.name);
        }}
        title={(s) => `Delete IP set ${s.name}?`}
        description={(s) =>
          `Proxmox deletes the IP set ${s.name} only if it has no entries left — remove its entries first. It does not check whether a rule still refers to the set. It cannot be undone: to get it back, create it again.`
        }
        confirmLabel="Delete IP Set"
      />
      {selectedSet && (
        <IPSetEntriesCard clusterId={clusterId} setName={selectedSet} />
      )}
    </div>
  );
}

function IPSetEntriesCard({
  clusterId,
  setName,
}: {
  clusterId: string;
  setName: string;
}) {
  const entriesQuery = useFirewallIPSetEntries(clusterId, setName);
  const addEntry = useAddFirewallIPSetEntry(clusterId, setName);
  const deleteEntry = useDeleteFirewallIPSetEntry(clusterId, setName);
  const [pendingDelete, setPendingDelete] = useState<FirewallIPSetEntry | null>(
    null,
  );
  const [cidr, setCidr] = useState("");

  const handleAdd = () => {
    addEntry.mutate(
      { cidr },
      {
        onSuccess: () => {
          setCidr("");
        },
      },
    );
  };

  return (
    <Card>
      <CardHeader>
        <CardTitle>Entries in &ldquo;{setName}&rdquo;</CardTitle>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="flex gap-2">
          <Input
            value={cidr}
            onChange={(e) => {
              setCidr(e.target.value);
            }}
            placeholder="CIDR (e.g. 10.0.0.0/8)"
            className="max-w-xs"
          />
          <Button
            size="sm"
            onClick={handleAdd}
            disabled={!cidr || addEntry.isPending}
          >
            Add
          </Button>
        </div>
        {entriesQuery.isLoading ? (
          <Skeleton className="h-16 w-full" />
        ) : entriesQuery.data && entriesQuery.data.length > 0 ? (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>CIDR</TableHead>
                <TableHead>No Match</TableHead>
                <TableHead>Comment</TableHead>
                <TableHead className="text-right">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {entriesQuery.data.map((e) => (
                <TableRow key={e.cidr}>
                  <TableCell>
                    <code className="text-xs">{e.cidr}</code>
                  </TableCell>
                  <TableCell>
                    {e.nomatch ? (
                      <Badge variant="secondary">nomatch</Badge>
                    ) : null}
                  </TableCell>
                  <TableCell className="text-muted-foreground">
                    {e.comment}
                  </TableCell>
                  <TableCell className="text-right">
                    <Button
                      aria-label={`Delete entry ${e.cidr}`}
                      variant="ghost"
                      size="sm"
                      onClick={() => {
                        setPendingDelete(e);
                      }}
                    >
                      <Trash2 className="h-4 w-4 text-destructive" />
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        ) : (
          <p className="text-sm text-muted-foreground">No entries.</p>
        )}
        {/* Proxmox's remove_ip (pve-firewall src/PVE/API2/Firewall/IPSet.pm)
            rewrites the set without that cidr; pve-firewall reads the change
            on its 10 s update loop ($updatetime in
            src/PVE/Service/pve_firewall.pm). A nomatch entry is written as
            "!cidr" and loaded into the kernel ipset as "add ... cidr nomatch"
            (src/PVE/Firewall.pm, the $entry->{nomatch} branches of the ipset
            writer and ruleset generator): an exclusion. Removing one LIFTS
            that exclusion, so the dialog branches on it. */}
        <ConfirmDeleteDialog
          target={pendingDelete}
          onClose={() => {
            setPendingDelete(null);
          }}
          onConfirm={(e) => {
            deleteEntry.mutate(e.cidr);
          }}
          title={(e) =>
            `Remove ${e.nomatch ? "!" : ""}${e.cidr} from IP set ${setName}?`
          }
          description={(e) =>
            e.nomatch
              ? `This is a nomatch entry: it excludes ${e.cidr} from the IP set ${setName}. Proxmox removes it at once, which lifts the exclusion — addresses in ${e.cidr} that another entry of the set covers start matching the set, and rules that use the set start applying to them on the firewall service's next update, every 10 seconds. It cannot be undone: to get it back, add the entry again with nomatch.`
              : `Proxmox removes ${e.cidr} from the IP set ${setName} at once; unless another entry of the set covers them, its addresses stop matching the set, and rules that use the set stop applying to them on the firewall service's next update, every 10 seconds. It cannot be undone: to get it back, add the entry again.`
          }
          confirmLabel="Remove Entry"
        />
      </CardContent>
    </Card>
  );
}

function FirewallSecurityGroupsSection({ clusterId }: { clusterId: string }) {
  const groupsQuery = useFirewallSecurityGroups(clusterId);
  const createGroup = useCreateFirewallSecurityGroup(clusterId);
  const deleteGroup = useDeleteFirewallSecurityGroup(clusterId);
  const [pendingDelete, setPendingDelete] =
    useState<FirewallSecurityGroup | null>(null);
  const [open, setOpen] = useState(false);
  const [group, setGroup] = useState("");
  const [comment, setComment] = useState("");

  const handleCreate = () => {
    const data: { group: string; comment?: string } = { group };
    if (comment) data.comment = comment;
    createGroup.mutate(data, {
      onSuccess: () => {
        setOpen(false);
        setGroup("");
        setComment("");
      },
    });
  };

  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between space-y-0">
        <CardTitle>Security Groups</CardTitle>
        <Dialog open={open} onOpenChange={setOpen}>
          <DialogTrigger asChild>
            <Button size="sm">
              <Plus className="mr-2 h-4 w-4" />
              Add Group
            </Button>
          </DialogTrigger>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>Create Security Group</DialogTitle>
            </DialogHeader>
            <div className="space-y-4">
              <div className="space-y-2">
                <Label>Name</Label>
                <Input
                  value={group}
                  onChange={(e) => {
                    setGroup(e.target.value);
                  }}
                />
              </div>
              <div className="space-y-2">
                <Label>Comment</Label>
                <Input
                  value={comment}
                  onChange={(e) => {
                    setComment(e.target.value);
                  }}
                />
              </div>
            </div>
            <DialogFooter>
              <Button
                onClick={handleCreate}
                disabled={!group || createGroup.isPending}
              >
                {createGroup.isPending ? "Creating..." : "Create"}
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      </CardHeader>
      <CardContent>
        {groupsQuery.isLoading ? (
          <Skeleton className="h-20 w-full" />
        ) : groupsQuery.data && groupsQuery.data.length > 0 ? (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Group</TableHead>
                <TableHead>Comment</TableHead>
                <TableHead className="text-right">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {groupsQuery.data.map((g) => (
                <TableRow key={g.group}>
                  <TableCell className="font-medium">{g.group}</TableCell>
                  <TableCell className="text-muted-foreground">
                    {g.comment}
                  </TableCell>
                  <TableCell className="text-right">
                    <Button
                      aria-label={`Delete security group ${g.group}`}
                      variant="ghost"
                      size="sm"
                      onClick={() => {
                        setPendingDelete(g);
                      }}
                    >
                      <Trash2 className="h-4 w-4 text-destructive" />
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        ) : (
          <p className="text-sm text-muted-foreground">
            No security groups defined.
          </p>
        )}
        {/* Proxmox's delete_security_group (pve-firewall
            src/PVE/API2/Firewall/Rules.pm) dies "Security group '<group>' is
            not empty" while the group has rules, and checks nothing else —
            not the cluster, node, guest or VNet rules that can refer to a
            group (src/PVE/Firewall.pm refuses a group rule only inside a
            group: $allow_groups). */}
        <ConfirmDeleteDialog
          target={pendingDelete}
          onClose={() => {
            setPendingDelete(null);
          }}
          onConfirm={(g) => {
            deleteGroup.mutate(g.group);
          }}
          title={(g) => `Delete security group ${g.group}?`}
          description={(g) =>
            `Proxmox deletes the security group ${g.group} only if it has no rules left — delete its rules first. It does not check whether any rule still refers to the group. It cannot be undone: to get it back, create it again.`
          }
          confirmLabel="Delete Group"
        />
      </CardContent>
    </Card>
  );
}

function FirewallLogSection({ clusterId }: { clusterId: string }) {
  const nodesQuery = useClusterNodes(clusterId);
  const nodes = nodesQuery.data ?? [];
  const [selectedNode, setSelectedNode] = useState("");
  const logQuery = useFirewallLog(clusterId, selectedNode);

  // Auto-select first node
  if (!selectedNode && nodes.length > 0 && nodes[0]) {
    setSelectedNode(nodes[0].name);
  }

  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between space-y-0">
        <CardTitle>Firewall Log</CardTitle>
        <div className="flex gap-2">
          {nodes.map((n) => (
            <Button
              key={n.name}
              size="sm"
              variant={selectedNode === n.name ? "default" : "outline"}
              onClick={() => {
                setSelectedNode(n.name);
              }}
            >
              {n.name}
            </Button>
          ))}
        </div>
      </CardHeader>
      <CardContent>
        {!selectedNode ? (
          <p className="text-sm text-muted-foreground">
            Select a node to view logs.
          </p>
        ) : logQuery.isLoading ? (
          <Skeleton className="h-32 w-full" />
        ) : logQuery.data && logQuery.data.length > 0 ? (
          <div className="max-h-96 overflow-y-auto rounded border bg-muted/50 p-2 font-mono text-xs">
            {logQuery.data.map((entry, i) => (
              <div key={i} className="whitespace-pre-wrap py-0.5">
                {entry.t}
              </div>
            ))}
          </div>
        ) : (
          <p className="text-sm text-muted-foreground">No log entries.</p>
        )}
      </CardContent>
    </Card>
  );
}
