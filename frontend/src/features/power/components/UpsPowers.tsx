import { useState } from "react";
import { Link } from "react-router-dom";
import { Plus, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Segmented } from "@/components/ui/segmented";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { ConfirmDeleteDialog } from "@/components/ConfirmDeleteDialog";
import { Panel } from "@/components/page/Panel";
import { useClusterNodes } from "@/features/clusters/api/cluster-queries";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import { usePermissions } from "@/hooks/usePermissions";
import { useSaveOutcome } from "@/hooks/useSaveOutcome";
import { describeError } from "@/lib/api-error";
import { formatDateTime, formatExactDateTime } from "@/lib/format";
import {
  useAddUpsFeed,
  useRemoveUpsFeed,
  useSetNutHostNode,
  useSetUpsFeedProtectedElsewhere,
  type UpsFeedRequest,
} from "../api/ups-queries";
import { feedName } from "../lib/ups-feeds";
import { upsOnBattery } from "../lib/ups-state";
import type { UpsDevice, UpsFeed } from "../types/ups";
import { ProtectionBadge } from "./UpsBadges";
import { useTranslation } from "react-i18next";

/** The small print under a flush table: its own gutter, a rule above. */
const NOTE = "border-t px-[18px] pt-3 text-[12.5px] text-muted-foreground";

/** When the logins were last listed, and that they hold during an outage. */
function ListedNote({ device }: { device: UpsDevice }) {
  const { t } = useTranslation("power");
  if (device.clients_listed_at === null) {
    return (
      <p className={NOTE}>
        {t("upsPowers.nexaraHasNotListedWho")}
      </p>
    );
  }
  return (
    <p className={NOTE}>
      {t("upsPowers.loginsAsOfClientsListedAt", { clientsListedAt: formatExactDateTime(device.clients_listed_at) })}
      {upsOnBattery(device) || device.state === "unknown"
        ? t("upsPowers.heldUntilTheUPSIs")
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
  const { t } = useTranslation("power");
  const { hasGlobalPermission } = usePermissions();
  const canManage = hasGlobalPermission("manage", "ups");
  const [adding, setAdding] = useState(false);
  // A fresh dialog per opening, kept mounted while it closes so a late answer is still reported.
  const [addKey, setAddKey] = useState(0);
  const [removing, setRemoving] = useState<UpsFeed | null>(null);
  const remove = useRemoveUpsFeed(device.id);
  const elsewhere = useSetUpsFeedProtectedElsewhere(device.id);

  return (
    // What it powers, wide, beside who is logged in and where its server runs.
    <div className="flex flex-wrap items-start gap-4">
      <Panel
        flush
        className="flex-[999_1_560px]"
        title={t("upsPowers.whatThisUPSPowers")}
        actions={
          canManage ? (
            <Button size="sm" variant="outline" className="gap-1.5" onClick={() => { setAddKey((k) => k + 1); setAdding(true); }}>
              <Plus className="h-4 w-4" aria-hidden="true" />
              {t("upsPowers.add")}
            </Button>
          ) : undefined
        }
      >
        {device.feeds.length === 0 ? (
          <p className="px-[18px] pb-1 text-sm text-muted-foreground">
            {canManage ? t("upsPowers.nothingYetNexaraFindsThe") : t("upsPowers.nothingYetFoundFromLogins")}
          </p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("upsPowers.powers")}</TableHead>
                <TableHead>{t("upsPowers.shutdown")}</TableHead>
                <TableHead className="hidden sm:table-cell">{t("upsPowers.howItWasAdded")}</TableHead>
                {canManage && <TableHead className="text-right">{t("upsPowers.protectedAnotherWay")}</TableHead>}
                {canManage && <TableHead className="w-10"><span className="sr-only">{t("upsPowers.remove")}</span></TableHead>}
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
                      <span className="ml-1.5 text-[12.5px] text-muted-foreground">{f.cluster_name}</span>
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
                      <span className="text-[12.5px] text-muted-foreground">{t("upsPowers.notANode")}</span>
                    )}
                  </TableCell>
                  <TableCell className="hidden text-[12.5px] text-muted-foreground sm:table-cell">
                    {f.source === "found" ? t("upsPowers.foundFromItsLogin") : t("upsPowers.addedByHand")}
                  </TableCell>
                  {canManage && (
                    <TableCell className="text-right">
                      {f.kind === "node" && (
                        <Switch
                          checked={f.protected_elsewhere}
                          aria-label={t("upsPowers.fShutsDownAnotherWay", { f: feedName(f) })}
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
                        className="h-8 w-8 text-muted-foreground hover:text-destructive"
                        aria-label={t("upsPowers.removeF", { f: feedName(f) })}
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
        )}
        {canManage && device.feeds.some((f) => f.kind === "node") && (
          <p className={NOTE}>
            {t("upsPowers.protectedAnotherWayTheNode")}
          </p>
        )}
      </Panel>

      <div className="flex min-w-0 flex-[1_1_340px] flex-col gap-4">
        <Panel flush title={t("upsPowers.loggedInToThisUPS")} aside={device.clients.length > 0 ? String(device.clients.length) : undefined}>
          {device.clients.length === 0 ? (
            <p className="px-[18px] pb-3 text-sm text-muted-foreground">{t("upsPowers.noUpsmonIsLoggedIn")}</p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t("upsPowers.address")}</TableHead>
                  <TableHead>{t("upsPowers.node")}</TableHead>
                  <TableHead>{t("upsPowers.seen")}</TableHead>
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
                    <TableCell className="text-[12.5px] text-muted-foreground">
                      {c.missed === 0
                        ? t("upsPowers.loggedIn")
                        : t("upsPowers.goneSinceLastSeenAt", { lastSeenAt: formatDateTime(c.last_seen_at, "—", "relative") })}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
          <ListedNote device={device} />
        </Panel>

        {/* Remounted when the stored host changes, so its selects start from it. */}
        <HostNodeCard
          key={`${device.host_node?.cluster_id ?? ""}/${device.host_node?.node_name ?? ""}`}
          device={device}
          canSet={canManage && hasGlobalPermission("view", "node")}
        />
      </div>

      <AddFeedDialog key={addKey} upsId={device.id} upsName={device.name} open={adding} onOpenChange={setAdding} />
      <ConfirmDeleteDialog
        target={removing}
        onClose={() => { setRemoving(null); }}
        onConfirm={(f) => { remove.mutate(f.id); }}
        title={(f) => t("upsPowers.removeFeed", { f: feedName(f) })}
        description={(f) =>
          f.kind === "node"
            ? t("upsPowers.nodeNoLongerListed", { nodeName: f.node_name, name: device.name })
            : t("upsPowers.loadNoLongerListed", { label: f.label, name: device.name })
        }
        confirmLabel={t("upsPowers.remove")}
      />
    </div>
  );
}

/** The node the NUT server runs on: its own upsmon, a loopback login, counts for that node. */
function HostNodeCard({ device, canSet }: { device: UpsDevice; canSet: boolean }) {
  const { t } = useTranslation("power");
  const host = device.host_node;
  const [cluster, setCluster] = useState(host?.cluster_id ?? "");
  const [node, setNode] = useState(host?.node_name ?? "");
  const clusters = useClusters();
  const nodes = useClusterNodes(canSet ? cluster : "");
  const setHost = useSetNutHostNode(device.server_id, device.id);
  if (!canSet && host === null) return null;
  const clusterName = clusters.data?.find((c) => c.id === host?.cluster_id)?.name;

  return (
    <Panel title={t("upsPowers.whereServerNameRuns", { serverName: device.server_name })}>
      <div className="space-y-3 text-sm">
        <p>
          {host === null
            ? t("upsPowers.nexaraDoesNotKnowWhich", { serverName: device.server_name })
            : clusterName === undefined
              ? t("upsPowers.serverRunsOn", { serverName: device.server_name, nodeName: host.node_name })
              : t("upsPowers.serverRunsOnInCluster", { serverName: device.server_name, nodeName: host.node_name, clusterName })}{" "}
          <span className="text-muted-foreground">
            {t("upsPowers.itsOwnUpsmonLogsIn")}
          </span>
        </p>
        {canSet && (
          <div className="flex flex-wrap items-end gap-2">
            <div className="min-w-0 flex-[1_1_8rem] space-y-1">
              <Label htmlFor="host-cluster">{t("upsPowers.cluster")}</Label>
              <Select value={cluster} onValueChange={(v) => { setCluster(v); setNode(""); }}>
                <SelectTrigger id="host-cluster"><SelectValue placeholder={t("upsPowers.cluster")} /></SelectTrigger>
                <SelectContent>
                  {(clusters.data ?? []).map((c) => <SelectItem key={c.id} value={c.id}>{c.name}</SelectItem>)}
                </SelectContent>
              </Select>
            </div>
            <div className="min-w-0 flex-[1_1_8rem] space-y-1">
              <Label htmlFor="host-node">{t("upsPowers.node")}</Label>
              <Select value={node} onValueChange={setNode} disabled={cluster === ""}>
                <SelectTrigger id="host-node"><SelectValue placeholder={t("upsPowers.node")} /></SelectTrigger>
                <SelectContent>
                  {(nodes.data ?? []).map((n) => <SelectItem key={n.id} value={n.name}>{n.name}</SelectItem>)}
                </SelectContent>
              </Select>
            </div>
            <div className="flex gap-2">
              <Button
                variant="outline"
                disabled={cluster === "" || node === "" || setHost.isPending}
                onClick={() => { setHost.mutate({ host_cluster_id: cluster, node_name: node }); }}
              >
                {t("upsPowers.set")}
              </Button>
              <Button variant="ghost" disabled={setHost.isPending} onClick={() => { setHost.mutate({ detect: true }); }}>
                {t("upsPowers.detect")}
              </Button>
            </div>
          </div>
        )}
      </div>
    </Panel>
  );
}

/** A node the UPS powers, chosen from the nodes the caller can see, or another load by name. */
function AddFeedDialog({ upsId, upsName, open, onOpenChange }: {
  upsId: string;
  upsName: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const { t } = useTranslation("power");
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
      action: t("upsPowers.addingToWhatUpsNamePowers", { upsName }),
      onSuccess: () => { onOpenChange(false); },
      onError: () => undefined, // shown below from the mutation's state
    });
  }

  const ready = kind === "node" ? cluster !== "" && node !== "" : label.trim() !== "";
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("upsPowers.addToWhatUpsNamePowers", { upsName })}</DialogTitle>
          <DialogDescription>
            {t("upsPowers.aNodeIsCheckedFor")}
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-4">
          <Segmented
            aria-label={t("upsPowers.whatToAdd")}
            size="md"
            value={kind}
            onValueChange={setKind}
            options={[
              { value: "node", label: t("upsPowers.aNode") },
              { value: "label", label: t("upsPowers.anotherLoad") },
            ]}
          />
          {kind === "node" ? (
            <div className="grid gap-3 sm:grid-cols-2">
              <div className="space-y-1">
                <Label htmlFor="feed-cluster">{t("upsPowers.cluster")}</Label>
                <Select value={cluster} onValueChange={(v) => { setCluster(v); setNode(""); }}>
                  <SelectTrigger id="feed-cluster"><SelectValue placeholder={t("upsPowers.cluster")} /></SelectTrigger>
                  <SelectContent>
                    {(clusters.data ?? []).map((c) => <SelectItem key={c.id} value={c.id}>{c.name}</SelectItem>)}
                  </SelectContent>
                </Select>
              </div>
              <div className="space-y-1">
                <Label htmlFor="feed-node">{t("upsPowers.node")}</Label>
                <Select value={node} onValueChange={setNode} disabled={cluster === ""}>
                  <SelectTrigger id="feed-node"><SelectValue placeholder={t("upsPowers.node")} /></SelectTrigger>
                  <SelectContent>
                    {(nodes.data ?? []).map((n) => <SelectItem key={n.id} value={n.name}>{n.name}</SelectItem>)}
                  </SelectContent>
                </Select>
              </div>
            </div>
          ) : (
            <div className="space-y-1">
              <Label htmlFor="feed-label">{t("upsPowers.name")}</Label>
              <Input id="feed-label" value={label} maxLength={64} placeholder={t("upsPowers.coreSwitch")} onChange={(e) => { setLabel(e.target.value); }} />
            </div>
          )}
          {add.error !== null && (
            <p role="alert" className="text-sm text-destructive">
              {describeError(add.error) || t("upsPowers.theRequestFailedCheckYour")}
            </p>
          )}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => { onOpenChange(false); }}>{t("upsPowers.cancel")}</Button>
          <Button disabled={!ready || add.isPending} onClick={submit}>{t("upsPowers.add")}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
