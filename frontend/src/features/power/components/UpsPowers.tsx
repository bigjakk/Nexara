import { useState } from "react";
import { Link } from "react-router-dom";
import { Plus, Trash2 } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { ConfirmDeleteDialog } from "@/components/ConfirmDeleteDialog";
import { useClusterNodes } from "@/features/clusters/api/cluster-queries";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import { usePermissions } from "@/hooks/usePermissions";
import { useSaveOutcome } from "@/hooks/useSaveOutcome";
import { describeError } from "@/lib/api-error";
import { formatDateTime, formatRelativeTime } from "@/lib/format";
import {
  useAddUpsFeed,
  useRemoveUpsFeed,
  useSetNutHostNode,
  useSetUpsFeedProtectedElsewhere,
  type UpsFeedRequest,
} from "../api/ups-queries";
import { feedName, protectionView, type ProtectionTone } from "../lib/ups-feeds";
import { upsOnBattery } from "../lib/ups-state";
import type { UpsDevice, UpsFeed } from "../types/ups";

const toneClass: Record<ProtectionTone, string> = {
  ok: "border-emerald-500/50 text-emerald-700 dark:text-emerald-400",
  warn: "border-amber-500/60 bg-amber-500/10 text-amber-700 dark:text-amber-400",
  muted: "text-muted-foreground",
};

/** A feed's verdict as a chip, with what it rests on as its title. */
export function ProtectionBadge({ feed }: { feed: UpsFeed }) {
  const v = protectionView(feed);
  return (
    <Badge variant="outline" className={toneClass[v.tone]} title={v.hint}>
      {v.label}
    </Badge>
  );
}

/** When the logins were last listed, and that they hold during an outage. */
function ListedNote({ device }: { device: UpsDevice }) {
  if (device.clients_listed_at === null) {
    return (
      <p className="text-xs text-muted-foreground">
        Nexara has not listed who is logged in to this UPS yet. It does so once a minute while the UPS is on mains.
      </p>
    );
  }
  return (
    <p className="text-xs text-muted-foreground">
      Logins as of {formatDateTime(device.clients_listed_at)}
      {upsOnBattery(device) || device.state === "unknown"
        ? ", held until the UPS is read on mains again: nodes log out on purpose as they shut down."
        : "."}
    </p>
  );
}

/**
 * What a UPS powers, who is logged in to it, and the node its NUT server runs
 * on. A node is protected when a upsmon on it is logged in, so it shuts down
 * before the battery runs out; Nexara finds nodes from those logins, and a
 * node with no upsmon, the one the check exists for, is added by hand.
 */
export function UpsPowers({ device }: { device: UpsDevice }) {
  const { hasGlobalPermission } = usePermissions();
  const canManage = hasGlobalPermission("manage", "ups");
  const [adding, setAdding] = useState(false);
  // A fresh dialog per opening, kept mounted while it closes so a late answer is still reported.
  const [addKey, setAddKey] = useState(0);
  const [removing, setRemoving] = useState<UpsFeed | null>(null);
  const remove = useRemoveUpsFeed(device.id);
  const elsewhere = useSetUpsFeedProtectedElsewhere(device.id);

  return (
    <div className="space-y-6">
      <Card>
        <CardHeader className="flex flex-row items-center justify-between gap-2 space-y-0 pb-2">
          <CardTitle className="text-sm font-medium">What this UPS powers</CardTitle>
          {canManage && (
            <Button size="sm" variant="outline" onClick={() => { setAddKey((k) => k + 1); setAdding(true); }}>
              <Plus className="mr-1 h-4 w-4" aria-hidden="true" />
              Add
            </Button>
          )}
        </CardHeader>
        <CardContent className="space-y-3">
          {device.feeds.length === 0 ? (
            <p className="text-sm text-muted-foreground">
              Nothing yet. Nexara finds the nodes this UPS powers from upsmon logins; add a node without upsmon by hand
              to have it checked, and other loads, such as a switch, to keep track of them.
            </p>
          ) : (
            <div className="overflow-x-auto">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Powers</TableHead>
                    <TableHead>Shutdown</TableHead>
                    <TableHead className="hidden sm:table-cell">How it was added</TableHead>
                    {canManage && <TableHead className="text-right">Protected another way</TableHead>}
                    {canManage && <TableHead className="w-10"><span className="sr-only">Remove</span></TableHead>}
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {device.feeds.map((f) => (
                    <TableRow key={f.id}>
                      <TableCell>
                        {f.kind === "node" && f.node_id !== null && f.cluster_id !== null ? (
                          <Link to={`/clusters/${f.cluster_id}/nodes/${f.node_id}`} className="font-medium hover:underline">
                            {f.node_name}
                          </Link>
                        ) : (
                          <span className="font-medium">{f.kind === "node" ? f.node_name : f.label}</span>
                        )}
                        {f.kind === "node" && f.cluster_name !== "" && (
                          <span className="ml-1.5 text-xs text-muted-foreground">{f.cluster_name}</span>
                        )}
                      </TableCell>
                      <TableCell>
                        {f.kind === "node" ? (
                          <div className="flex flex-col items-start gap-0.5">
                            <ProtectionBadge feed={f} />
                            {f.login_address !== "" && (
                              <span className="font-mono text-xs text-muted-foreground">{f.login_address}</span>
                            )}
                          </div>
                        ) : (
                          <span className="text-xs text-muted-foreground">Not a node</span>
                        )}
                      </TableCell>
                      <TableCell className="hidden text-xs text-muted-foreground sm:table-cell">
                        {f.source === "found" ? "Found from its login" : "Added by hand"}
                      </TableCell>
                      {canManage && (
                        <TableCell className="text-right">
                          {f.kind === "node" && (
                            <Switch
                              checked={f.protected_elsewhere}
                              aria-label={`${feedName(f)} shuts down another way`}
                              disabled={elsewhere.isPending}
                              onCheckedChange={(on) => { elsewhere.mutate({ feedId: f.id, protectedElsewhere: on }); }}
                            />
                          )}
                        </TableCell>
                      )}
                      {canManage && (
                        <TableCell>
                          <Button
                            size="icon"
                            variant="ghost"
                            aria-label={`Remove ${feedName(f)}`}
                            onClick={() => { setRemoving(f); }}
                          >
                            <Trash2 className="h-4 w-4" aria-hidden="true" />
                          </Button>
                        </TableCell>
                      )}
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          )}
          {canManage && device.feeds.some((f) => f.kind === "node") && (
            <p className="text-xs text-muted-foreground">
              Protected another way: the node shuts down some way Nexara cannot see, such as a upsmon behind NAT or a
              proxy, or another tool, so it is not warned about.
            </p>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="pb-2">
          <CardTitle className="text-sm font-medium">Logged in to this UPS</CardTitle>
        </CardHeader>
        <CardContent className="space-y-3">
          {device.clients.length === 0 ? (
            <p className="text-sm text-muted-foreground">No upsmon is logged in to this UPS.</p>
          ) : (
            <div className="overflow-x-auto">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Address</TableHead>
                    <TableHead>Node</TableHead>
                    <TableHead>Seen</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {device.clients.map((c) => (
                    <TableRow key={c.address}>
                      <TableCell className="font-mono text-xs">
                        {c.address}
                        {c.connections > 1 && <span className="ml-1.5 text-muted-foreground">×{c.connections}</span>}
                      </TableCell>
                      <TableCell>{c.node_name || <span className="text-muted-foreground">—</span>}</TableCell>
                      <TableCell className="text-xs text-muted-foreground">
                        {c.missed === 0
                          ? "Logged in"
                          : `Gone since ${formatRelativeTime(c.last_seen_at)}`}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          )}
          <ListedNote device={device} />
        </CardContent>
      </Card>

      {/* Remounted when the stored host changes, so its selects start from it. */}
      <HostNodeCard
        key={`${device.host_node?.cluster_id ?? ""}/${device.host_node?.node_name ?? ""}`}
        device={device}
        canSet={canManage && hasGlobalPermission("view", "node")}
      />

      <AddFeedDialog key={addKey} upsId={device.id} upsName={device.name} open={adding} onOpenChange={setAdding} />
      <ConfirmDeleteDialog
        target={removing}
        onClose={() => { setRemoving(null); }}
        onConfirm={(f) => { remove.mutate(f.id); }}
        title={(f) => `Remove ${feedName(f)}?`}
        description={(f) =>
          f.kind === "node"
            ? `${f.node_name} is no longer listed as powered by ${device.name}, and is not checked for it. A upsmon login from it will not add it back; only adding it by hand does.`
            : `${f.label} is no longer listed as powered by ${device.name}.`
        }
        confirmLabel="Remove"
      />
    </div>
  );
}

/** The node the NUT server runs on: its own upsmon, a loopback login, counts for that node. */
function HostNodeCard({ device, canSet }: { device: UpsDevice; canSet: boolean }) {
  const host = device.host_node;
  const [cluster, setCluster] = useState(host?.cluster_id ?? "");
  const [node, setNode] = useState(host?.node_name ?? "");
  const clusters = useClusters();
  const nodes = useClusterNodes(canSet ? cluster : "");
  const setHost = useSetNutHostNode(device.server_id, device.id);
  if (!canSet && host === null) return null;
  const clusterName = clusters.data?.find((c) => c.id === host?.cluster_id)?.name;

  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="text-sm font-medium">Where {device.server_name} runs</CardTitle>
      </CardHeader>
      <CardContent className="space-y-3 text-sm">
        <p>
          {host === null
            ? `Nexara does not know which node ${device.server_name} runs on, if any.`
            : `${device.server_name} runs on ${host.node_name}${clusterName === undefined ? "" : ` (${clusterName})`}.`}{" "}
          <span className="text-muted-foreground">
            Its own upsmon logs in over loopback, and that login counts for the node it runs on.
          </span>
        </p>
        {canSet && (
          <div className="flex flex-wrap items-end gap-2">
            <div className="space-y-1">
              <Label htmlFor="host-cluster">Cluster</Label>
              <Select value={cluster} onValueChange={(v) => { setCluster(v); setNode(""); }}>
                <SelectTrigger id="host-cluster" className="w-44"><SelectValue placeholder="Cluster" /></SelectTrigger>
                <SelectContent>
                  {(clusters.data ?? []).map((c) => <SelectItem key={c.id} value={c.id}>{c.name}</SelectItem>)}
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-1">
              <Label htmlFor="host-node">Node</Label>
              <Select value={node} onValueChange={setNode} disabled={cluster === ""}>
                <SelectTrigger id="host-node" className="w-44"><SelectValue placeholder="Node" /></SelectTrigger>
                <SelectContent>
                  {(nodes.data ?? []).map((n) => <SelectItem key={n.id} value={n.name}>{n.name}</SelectItem>)}
                </SelectContent>
              </Select>
            </div>
            <Button
              size="sm"
              disabled={cluster === "" || node === "" || setHost.isPending}
              onClick={() => { setHost.mutate({ host_cluster_id: cluster, node_name: node }); }}
            >
              Set
            </Button>
            <Button size="sm" variant="outline" disabled={setHost.isPending} onClick={() => { setHost.mutate({ detect: true }); }}>
              Detect
            </Button>
          </div>
        )}
      </CardContent>
    </Card>
  );
}

/** A node the UPS powers, chosen from the nodes the caller can see, or another load by name. */
function AddFeedDialog({ upsId, upsName, open, onOpenChange }: {
  upsId: string;
  upsName: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const [kind, setKind] = useState<"node" | "label">("node");
  const [cluster, setCluster] = useState("");
  const [node, setNode] = useState("");
  const [label, setLabel] = useState("");
  const clusters = useClusters();
  const nodes = useClusterNodes(open ? cluster : "");
  const add = useAddUpsFeed(upsId);
  const settle = useSaveOutcome(open);

  function submit() {
    const req: UpsFeedRequest = kind === "node" ? { feed_cluster_id: cluster, node_name: node } : { label: label.trim() };
    settle(add.mutateAsync(req), {
      action: `Adding to what ${upsName} powers`,
      onSuccess: () => { onOpenChange(false); },
      onError: () => undefined, // shown below from the mutation's state
    });
  }

  const ready = kind === "node" ? cluster !== "" && node !== "" : label.trim() !== "";
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Add to what {upsName} powers</DialogTitle>
          <DialogDescription>
            A node is checked for a upsmon logged in to this UPS. A node with upsmon is found on its own.
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-4">
          <div className="flex gap-2">
            <Button size="sm" aria-pressed={kind === "node"} variant={kind === "node" ? "default" : "outline"} onClick={() => { setKind("node"); }}>A node</Button>
            <Button size="sm" aria-pressed={kind === "label"} variant={kind === "label" ? "default" : "outline"} onClick={() => { setKind("label"); }}>Another load</Button>
          </div>
          {kind === "node" ? (
            <div className="grid gap-3 sm:grid-cols-2">
              <div className="space-y-1">
                <Label htmlFor="feed-cluster">Cluster</Label>
                <Select value={cluster} onValueChange={(v) => { setCluster(v); setNode(""); }}>
                  <SelectTrigger id="feed-cluster"><SelectValue placeholder="Cluster" /></SelectTrigger>
                  <SelectContent>
                    {(clusters.data ?? []).map((c) => <SelectItem key={c.id} value={c.id}>{c.name}</SelectItem>)}
                  </SelectContent>
                </Select>
              </div>
              <div className="space-y-1">
                <Label htmlFor="feed-node">Node</Label>
                <Select value={node} onValueChange={setNode} disabled={cluster === ""}>
                  <SelectTrigger id="feed-node"><SelectValue placeholder="Node" /></SelectTrigger>
                  <SelectContent>
                    {(nodes.data ?? []).map((n) => <SelectItem key={n.id} value={n.name}>{n.name}</SelectItem>)}
                  </SelectContent>
                </Select>
              </div>
            </div>
          ) : (
            <div className="space-y-1">
              <Label htmlFor="feed-label">Name</Label>
              <Input id="feed-label" value={label} maxLength={64} placeholder="Core switch" onChange={(e) => { setLabel(e.target.value); }} />
            </div>
          )}
          {add.error !== null && (
            <p role="alert" className="text-sm text-destructive">
              {describeError(add.error) || "The request failed — check your connection and try again."}
            </p>
          )}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => { onOpenChange(false); }}>Cancel</Button>
          <Button disabled={!ready || add.isPending} onClick={submit}>Add</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
