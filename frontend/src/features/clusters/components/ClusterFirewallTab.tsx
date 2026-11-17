import { useState } from "react";
import { Tabs, TabsContent } from "@/components/ui/tabs";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import { Panel } from "@/components/page/Panel";
import { Segmented } from "@/components/ui/segmented";
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
import { FirewallStatusStrip } from "@/features/networks/components/FirewallStatusStrip";
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
import { HistoryTabContent } from "@/features/history/components/HistoryTab";
import { useCanViewHistory } from "@/features/history/hooks/useCanViewHistory";
import { ClusterHistoryButton } from "@/features/history/components/ClusterHistoryButton";
import { clusterHistories } from "@/features/history/lib/cluster-histories";

interface ClusterFirewallTabProps {
  clusterId: string;
}

/** The views of the tab, in the order the switch shows them. */
const VIEWS = [
  { value: "rules", label: "Rules" },
  { value: "templates", label: "Templates" },
  { value: "aliases", label: "Aliases" },
  { value: "ipsets", label: "IP sets" },
  { value: "groups", label: "Security groups" },
  { value: "log", label: "Log" },
  { value: "options", label: "Options" },
] as const;

/** Count of a list for a panel's aside: "3 aliases", or nothing while unread. */
function countLabel(
  count: number | undefined,
  one: string,
  many: string,
): string | undefined {
  if (count === undefined || count === 0) return undefined;
  return `${String(count)} ${count === 1 ? one : many}`;
}

/** The trash button at the end of a row: quiet until it is pointed at. */
const ROW_DELETE_CLASS = "h-8 w-8 text-muted-foreground hover:text-destructive";

export function ClusterFirewallTab({ clusterId }: ClusterFirewallTabProps) {
  // The panels are Radix tab panels, so only the view on show is mounted and
  // reads its data; the switch above them is what picks it.
  const [view, setView] = useState("rules");
  const history = clusterHistories.firewall(clusterId);
  const canHistory = useCanViewHistory(
    history.permission,
    history.target.clusterId === null,
  );
  const shown = view === "history" && !canHistory ? "rules" : view;

  return (
    <div className="space-y-4">
      <FirewallStatusStrip clusterId={clusterId} />
      <Tabs value={shown} onValueChange={setView}>
        <Segmented
          aria-label="Firewall view"
          size="md"
          value={shown}
          onValueChange={setView}
          options={[
            ...VIEWS,
            ...(canHistory ? [{ value: "history", label: "History" }] : []),
          ]}
        />
        {/* Each panel is named by its view: a dialog whose opener is gone
            when it closes (a deleted row) sends focus to the panel. */}
        <TabsContent
          value="rules"
          aria-label="Rules"
          aria-labelledby={undefined}
        >
          <FirewallRulesTable clusterId={clusterId} />
        </TabsContent>
        <TabsContent
          value="templates"
          aria-label="Templates"
          aria-labelledby={undefined}
        >
          <FirewallTemplatesTable clusterId={clusterId} />
        </TabsContent>
        <TabsContent
          value="aliases"
          aria-label="Aliases"
          aria-labelledby={undefined}
        >
          <FirewallAliasesSection clusterId={clusterId} />
        </TabsContent>
        <TabsContent
          value="ipsets"
          aria-label="IP sets"
          aria-labelledby={undefined}
        >
          <FirewallIPSetsSection clusterId={clusterId} />
        </TabsContent>
        <TabsContent
          value="groups"
          aria-label="Security groups"
          aria-labelledby={undefined}
        >
          <FirewallSecurityGroupsSection clusterId={clusterId} />
        </TabsContent>
        <TabsContent value="log" aria-label="Log" aria-labelledby={undefined}>
          <FirewallLogSection clusterId={clusterId} />
        </TabsContent>
        <TabsContent
          value="options"
          aria-label="Options"
          aria-labelledby={undefined}
        >
          <FirewallOptionsCard clusterId={clusterId} />
        </TabsContent>
        <HistoryTabContent history={history} label="History" />
      </Tabs>
    </div>
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
    <Panel
      flush
      title="Aliases"
      aside={countLabel(aliasesQuery.data?.length, "alias", "aliases")}
      actions={
        <Dialog open={open} onOpenChange={setOpen}>
          <DialogTrigger asChild>
            <Button size="sm">
              <Plus className="h-4 w-4" />
              Add alias
            </Button>
          </DialogTrigger>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>Add alias</DialogTitle>
            </DialogHeader>
            <div className="space-y-4">
              <div className="space-y-2">
                <Label htmlFor="fw-alias-name">Name</Label>
                <Input
                  id="fw-alias-name"
                  value={name}
                  onChange={(e) => {
                    setName(e.target.value);
                  }}
                />
              </div>
              <div className="space-y-2">
                <Label htmlFor="fw-alias-cidr">CIDR</Label>
                <Input
                  id="fw-alias-cidr"
                  value={cidr}
                  onChange={(e) => {
                    setCidr(e.target.value);
                  }}
                  placeholder="192.168.1.0/24"
                />
              </div>
              <div className="space-y-2">
                <Label htmlFor="fw-alias-comment">Comment</Label>
                <Input
                  id="fw-alias-comment"
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
      }
    >
      <>
        {aliasesQuery.isLoading ? (
          <div className="px-[18px] pb-1">
            <Skeleton className="h-20 w-full" />
          </div>
        ) : aliasesQuery.data && aliasesQuery.data.length > 0 ? (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>CIDR</TableHead>
                <TableHead>Comment</TableHead>
                <TableHead className="w-0">
                  <span className="sr-only">Actions</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {aliasesQuery.data.map((a) => (
                <TableRow key={a.name}>
                  <TableCell className="font-medium">
                    {a.name}
                    <ClusterHistoryButton
                      history={clusterHistories.firewallAlias(
                        clusterId,
                        a.name,
                      )}
                      title={a.name}
                    />
                  </TableCell>
                  <TableCell className="font-mono text-[12.5px]">
                    {a.cidr}
                  </TableCell>
                  <TableCell className="text-muted-foreground">
                    {a.comment}
                  </TableCell>
                  <TableCell className="py-1 text-right">
                    <Button
                      aria-label={`Delete alias ${a.name}`}
                      variant="ghost"
                      size="icon"
                      className={ROW_DELETE_CLASS}
                      onClick={() => {
                        setPendingDelete(a);
                      }}
                    >
                      <Trash2 className="h-4 w-4" />
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        ) : (
          <p className="px-[18px] pb-1 text-sm text-muted-foreground">
            No aliases defined.
          </p>
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
          confirmLabel="Delete alias"
        />
      </>
    </Panel>
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
      <Panel
        flush
        title="IP sets"
        aside={
          setsQuery.data && setsQuery.data.length > 0
            ? `${String(setsQuery.data.length)} ${setsQuery.data.length === 1 ? "set" : "sets"}. Select one to see its entries.`
            : undefined
        }
        actions={
          <Dialog open={open} onOpenChange={setOpen}>
            <DialogTrigger asChild>
              <Button size="sm">
                <Plus className="h-4 w-4" />
                Add IP set
              </Button>
            </DialogTrigger>
            <DialogContent>
              <DialogHeader>
                <DialogTitle>Add IP set</DialogTitle>
              </DialogHeader>
              <div className="space-y-4">
                <div className="space-y-2">
                  <Label htmlFor="fw-ipset-name">Name</Label>
                  <Input
                    id="fw-ipset-name"
                    value={name}
                    onChange={(e) => {
                      setName(e.target.value);
                    }}
                  />
                </div>
                <div className="space-y-2">
                  <Label htmlFor="fw-ipset-comment">Comment</Label>
                  <Input
                    id="fw-ipset-comment"
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
        }
      >
        <>
          {setsQuery.isLoading ? (
            <div className="px-[18px] pb-1">
              <Skeleton className="h-20 w-full" />
            </div>
          ) : setsQuery.data && setsQuery.data.length > 0 ? (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Name</TableHead>
                  <TableHead>Comment</TableHead>
                  <TableHead className="w-0">
                    <span className="sr-only">Actions</span>
                  </TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {setsQuery.data.map((s) => (
                  <TableRow
                    key={s.name}
                    className="cursor-pointer"
                    data-state={selectedSet === s.name ? "selected" : undefined}
                    onClick={() => {
                      setSelectedSet(selectedSet === s.name ? "" : s.name);
                    }}
                  >
                    <TableCell className="font-medium">
                      {s.name}
                      <ClusterHistoryButton
                        history={clusterHistories.firewallIpset(
                          clusterId,
                          s.name,
                        )}
                        title={s.name}
                      />
                    </TableCell>
                    <TableCell className="text-muted-foreground">
                      {s.comment}
                    </TableCell>
                    <TableCell className="py-1 text-right">
                      <Button
                        aria-label={`Delete IPSet ${s.name}`}
                        variant="ghost"
                        size="icon"
                        className={ROW_DELETE_CLASS}
                        onClick={(e) => {
                          e.stopPropagation();
                          setPendingDelete(s);
                        }}
                      >
                        <Trash2 className="h-4 w-4" />
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          ) : (
            <p className="px-[18px] pb-1 text-sm text-muted-foreground">
              No IP sets defined.
            </p>
          )}
        </>
      </Panel>
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
        confirmLabel="Delete IP set"
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
    <Panel
      flush
      title={<>Entries in &ldquo;{setName}&rdquo;</>}
      aside={countLabel(entriesQuery.data?.length, "entry", "entries")}
      actions={
        <>
          <Input
            aria-label="CIDR to add"
            value={cidr}
            onChange={(e) => {
              setCidr(e.target.value);
            }}
            placeholder="CIDR (e.g. 10.0.0.0/8)"
            className="h-8 w-56 max-w-full"
          />
          <Button
            size="sm"
            variant="outline"
            onClick={handleAdd}
            disabled={!cidr || addEntry.isPending}
          >
            Add
          </Button>
        </>
      }
    >
      <>
        {entriesQuery.isLoading ? (
          <div className="px-[18px] pb-1">
            <Skeleton className="h-16 w-full" />
          </div>
        ) : entriesQuery.data && entriesQuery.data.length > 0 ? (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>CIDR</TableHead>
                <TableHead>Match</TableHead>
                <TableHead>Comment</TableHead>
                <TableHead className="w-0">
                  <span className="sr-only">Actions</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {entriesQuery.data.map((e) => (
                <TableRow key={e.cidr}>
                  <TableCell className="font-mono text-[12.5px]">
                    {e.cidr}
                  </TableCell>
                  <TableCell>
                    {e.nomatch ? (
                      <Badge variant="secondary">nomatch</Badge>
                    ) : null}
                  </TableCell>
                  <TableCell className="text-muted-foreground">
                    {e.comment}
                  </TableCell>
                  <TableCell className="py-1 text-right">
                    <Button
                      aria-label={`Delete entry ${e.cidr}`}
                      variant="ghost"
                      size="icon"
                      className={ROW_DELETE_CLASS}
                      onClick={() => {
                        setPendingDelete(e);
                      }}
                    >
                      <Trash2 className="h-4 w-4" />
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        ) : (
          <p className="px-[18px] pb-1 text-sm text-muted-foreground">
            No entries.
          </p>
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
          confirmLabel="Remove entry"
        />
      </>
    </Panel>
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
    <Panel
      flush
      title="Security groups"
      aside={countLabel(groupsQuery.data?.length, "group", "groups")}
      actions={
        <Dialog open={open} onOpenChange={setOpen}>
          <DialogTrigger asChild>
            <Button size="sm">
              <Plus className="h-4 w-4" />
              Add group
            </Button>
          </DialogTrigger>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>Add security group</DialogTitle>
            </DialogHeader>
            <div className="space-y-4">
              <div className="space-y-2">
                <Label htmlFor="fw-group-name">Name</Label>
                <Input
                  id="fw-group-name"
                  value={group}
                  onChange={(e) => {
                    setGroup(e.target.value);
                  }}
                />
              </div>
              <div className="space-y-2">
                <Label htmlFor="fw-group-comment">Comment</Label>
                <Input
                  id="fw-group-comment"
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
      }
    >
      <>
        {groupsQuery.isLoading ? (
          <div className="px-[18px] pb-1">
            <Skeleton className="h-20 w-full" />
          </div>
        ) : groupsQuery.data && groupsQuery.data.length > 0 ? (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Group</TableHead>
                <TableHead>Comment</TableHead>
                <TableHead className="w-0">
                  <span className="sr-only">Actions</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {groupsQuery.data.map((g) => (
                <TableRow key={g.group}>
                  <TableCell className="font-medium">
                    {g.group}
                    <ClusterHistoryButton
                      history={clusterHistories.firewallGroup(
                        clusterId,
                        g.group,
                      )}
                      title={g.group}
                    />
                  </TableCell>
                  <TableCell className="text-muted-foreground">
                    {g.comment}
                  </TableCell>
                  <TableCell className="py-1 text-right">
                    <Button
                      aria-label={`Delete security group ${g.group}`}
                      variant="ghost"
                      size="icon"
                      className={ROW_DELETE_CLASS}
                      onClick={() => {
                        setPendingDelete(g);
                      }}
                    >
                      <Trash2 className="h-4 w-4" />
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        ) : (
          <p className="px-[18px] pb-1 text-sm text-muted-foreground">
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
          confirmLabel="Delete group"
        />
      </>
    </Panel>
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
    <Panel
      flush
      title="Firewall log"
      aside={
        selectedNode !== "" && logQuery.data && logQuery.data.length > 0
          ? `${String(logQuery.data.length)} ${logQuery.data.length === 1 ? "line" : "lines"} on ${selectedNode}`
          : undefined
      }
      actions={
        nodes.length > 0 ? (
          <Segmented
            aria-label="Node"
            value={selectedNode}
            onValueChange={setSelectedNode}
            options={nodes.map((n) => ({ value: n.name, label: n.name }))}
          />
        ) : undefined
      }
    >
      {!selectedNode ? (
        <p className="px-[18px] pb-1 text-sm text-muted-foreground">
          Select a node to view logs.
        </p>
      ) : logQuery.isLoading ? (
        <div className="px-[18px] pb-1">
          <Skeleton className="h-32 w-full" />
        </div>
      ) : logQuery.data && logQuery.data.length > 0 ? (
        <div className="max-h-[28rem] overflow-y-auto border-t px-[18px] pt-3 font-mono text-[12.5px] leading-relaxed">
          {logQuery.data.map((entry, i) => (
            <div
              key={i}
              className="whitespace-pre-wrap [overflow-wrap:anywhere]"
            >
              {entry.t}
            </div>
          ))}
        </div>
      ) : (
        <p className="px-[18px] pb-1 text-sm text-muted-foreground">
          No log entries.
        </p>
      )}
    </Panel>
  );
}
