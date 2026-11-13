import { useState, type ReactNode } from "react";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogDescription,
} from "@/components/ui/dialog";
import { Panel } from "@/components/page/Panel";
import { Segmented } from "@/components/ui/segmented";
import { Skeleton } from "@/components/ui/skeleton";
import { Tabs, TabsContent } from "@/components/ui/tabs";
import { Trash2, ChevronDown, ChevronRight, RefreshCw } from "lucide-react";
import {
  useSDNZones,
  useSDNVNets,
  useDeleteSDNZone,
  useDeleteSDNVNet,
  useDeleteSDNSubnet,
  useSDNSubnets,
  useApplySDN,
  useSDNControllers,
  useDeleteSDNController,
  useSDNIPAMs,
  useDeleteSDNIPAM,
  useSDNDNSPlugins,
  useDeleteSDNDNS,
} from "../api/network-queries";
import { ConfirmDeleteDialog } from "@/components/ConfirmDeleteDialog";
import { ClusterHistoryButton } from "@/features/history/components/ClusterHistoryButton";
import { clusterHistories } from "@/features/history/lib/cluster-histories";
import { CreateSDNZoneDialog } from "./CreateSDNZoneDialog";
import { CreateSDNVNetDialog } from "./CreateSDNVNetDialog";
import { CreateSDNSubnetDialog } from "./CreateSDNSubnetDialog";
import { CreateSDNControllerDialog } from "./CreateSDNControllerDialog";
import { CreateSDNIPAMDialog } from "./CreateSDNIPAMDialog";
import { CreateSDNDNSDialog } from "./CreateSDNDNSDialog";
import type { SDNSubnet, SDNVNet } from "../types/network";

interface SDNTableProps {
  clusterId: string;
}

/** The trash button at the end of a row: quiet until it is pointed at. */
const ROW_DELETE_CLASS = "h-8 w-8 text-muted-foreground hover:text-destructive";

/** A notice in place of a table, inside the flush panel's gutters. */
function Note({ children }: { children: ReactNode }) {
  return (
    <p className="px-[18px] pb-1 text-sm text-muted-foreground">{children}</p>
  );
}

interface DeleteConfirmButtonProps {
  name: string;
  kind: string;
  onConfirm: () => void;
  isPending: boolean;
}

function DeleteConfirmButton({
  name,
  kind,
  onConfirm,
  isPending,
}: DeleteConfirmButtonProps) {
  const [open, setOpen] = useState(false);

  return (
    <>
      <Button
        aria-label={`Delete ${kind} ${name}`}
        variant="ghost"
        size="icon"
        className={ROW_DELETE_CLASS}
        onClick={() => {
          setOpen(true);
        }}
      >
        <Trash2 className="h-4 w-4" />
      </Button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Delete {kind}</DialogTitle>
            <DialogDescription>
              Are you sure you want to delete {kind} &quot;{name}&quot;? This
              action cannot be undone.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button
              variant="outline"
              onClick={() => {
                setOpen(false);
              }}
            >
              Cancel
            </Button>
            <Button
              variant="destructive"
              onClick={() => {
                onConfirm();
                setOpen(false);
              }}
              disabled={isPending}
            >
              Delete
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}

function VNetSubnetsRow({
  clusterId,
  vnet,
}: {
  clusterId: string;
  vnet: SDNVNet;
}) {
  const [expanded, setExpanded] = useState(false);
  const { data: subnets, isLoading } = useSDNSubnets(
    clusterId,
    expanded ? vnet.vnet : "",
  );
  const deleteSubnet = useDeleteSDNSubnet(clusterId, vnet.vnet);
  const deleteVNet = useDeleteSDNVNet(clusterId);
  const [pendingSubnetDelete, setPendingSubnetDelete] =
    useState<SDNSubnet | null>(null);

  return (
    <>
      <TableRow
        className="cursor-pointer"
        onClick={() => {
          setExpanded(!expanded);
        }}
      >
        <TableCell className="w-8 text-muted-foreground">
          {expanded ? (
            <ChevronDown className="h-4 w-4" />
          ) : (
            <ChevronRight className="h-4 w-4" />
          )}
        </TableCell>
        <TableCell className="font-medium">
          {vnet.vnet}
          <ClusterHistoryButton
            history={clusterHistories.sdnVnet(clusterId, vnet.vnet)}
            title={vnet.vnet}
          />
        </TableCell>
        <TableCell>{vnet.zone}</TableCell>
        <TableCell>{vnet.tag ?? "-"}</TableCell>
        <TableCell>{vnet.alias || "-"}</TableCell>
        <TableCell
          className="py-1 text-right"
          onClick={(e) => {
            e.stopPropagation();
          }}
        >
          <div className="flex justify-end gap-1">
            <CreateSDNVNetDialog clusterId={clusterId} initialData={vnet} />
            <DeleteConfirmButton
              name={vnet.vnet}
              kind="VNet"
              onConfirm={() => {
                deleteVNet.mutate(vnet.vnet);
              }}
              isPending={deleteVNet.isPending}
            />
          </div>
        </TableCell>
      </TableRow>
      {expanded && (
        <TableRow className="hover:bg-transparent">
          <TableCell
            colSpan={6}
            className="bg-foreground/[0.03] py-3 pl-[3.25rem]"
          >
            <div className="space-y-2">
              <div className="flex items-center justify-between gap-2">
                <span className="text-sm font-semibold">Subnets</span>
                <CreateSDNSubnetDialog clusterId={clusterId} vnet={vnet.vnet} />
              </div>
              {isLoading ? (
                <p className="text-sm text-muted-foreground">Loading...</p>
              ) : !subnets || subnets.length === 0 ? (
                <p className="text-sm text-muted-foreground">
                  No subnets configured.
                </p>
              ) : (
                <div>
                  <Table>
                    <TableHeader>
                      <TableRow>
                        <TableHead className="pl-0">Subnet</TableHead>
                        <TableHead>Gateway</TableHead>
                        <TableHead>SNAT</TableHead>
                        <TableHead className="w-0 pr-0">
                          <span className="sr-only">Actions</span>
                        </TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {subnets.map((s) => (
                        <TableRow key={s.subnet}>
                          <TableCell className="pl-0 font-mono text-[12.5px] font-medium">
                            {s.subnet}
                            <ClusterHistoryButton
                              history={clusterHistories.sdnSubnet(
                                clusterId,
                                vnet.vnet,
                                s.subnet,
                              )}
                              title={s.subnet}
                            />
                          </TableCell>
                          <TableCell className="font-mono text-[12.5px]">
                            {s.gateway || "-"}
                          </TableCell>
                          <TableCell>{s.snat === 1 ? "Yes" : "No"}</TableCell>
                          <TableCell className="py-1 pr-0 text-right">
                            <div className="flex justify-end gap-1">
                              <CreateSDNSubnetDialog
                                clusterId={clusterId}
                                vnet={vnet.vnet}
                                initialData={s}
                              />
                              <Button
                                aria-label={`Delete Subnet ${s.subnet}`}
                                variant="ghost"
                                size="icon"
                                className={ROW_DELETE_CLASS}
                                onClick={() => {
                                  setPendingSubnetDelete(s);
                                }}
                                disabled={deleteSubnet.isPending}
                              >
                                <Trash2 className="h-4 w-4" />
                              </Button>
                            </div>
                          </TableCell>
                        </TableRow>
                      ))}
                    </TableBody>
                  </Table>
                </div>
              )}
            </div>
            {/* What Proxmox does, transcribed: the subnet DELETE (pve-network
                src/PVE/API2/Network/SDN/Subnets.pm) calls
                Subnets::del_subnet, which — only when the zone has an IPAM —
                removes it from that IPAM at once; the built-in PVE IPAM
                (Ipams/PVEPlugin.pm del_subnet) dies "cannot delete subnet
                ..., not empty" while any address other than the gateway is
                allocated. It then drops the subnet from sdn/subnets.cfg.
                Nodes build their SDN network from the running config, which
                only the SDN apply (PUT /cluster/sdn, "Apply sdn controller
                changes && reload") replaces — the Apply Changes button. The
                SDN API's rollback (POST /cluster/sdn/rollback, which Nexara
                does not call) rewrites subnets.cfg from the running config
                but does not touch the IPAM, so the dialog promises no undo. */}
            <ConfirmDeleteDialog
              target={pendingSubnetDelete}
              onClose={() => {
                setPendingSubnetDelete(null);
              }}
              onConfirm={(subnet) => {
                deleteSubnet.mutate(subnet.subnet);
              }}
              title={(subnet) => `Delete subnet ${subnet.subnet}?`}
              description={(subnet) => (
                <>
                  <span className="block">
                    Proxmox removes subnet {subnet.subnet} from VNet {vnet.vnet}{" "}
                    in the SDN configuration now. If the zone uses an IPAM, the
                    subnet is removed from it immediately too; the built-in PVE
                    IPAM refuses while addresses other than the gateway are
                    still allocated in the subnet.
                  </span>
                  <span className="mt-2 block">
                    The nodes keep using the subnet until SDN changes are
                    applied (Apply Changes). Nexara cannot undo the delete: to
                    get the subnet back, create it again.
                  </span>
                </>
              )}
              confirmLabel="Delete Subnet"
            />
          </TableCell>
        </TableRow>
      )}
    </>
  );
}

function ControllersTab({ clusterId }: { clusterId: string }) {
  const { data: controllers, isLoading } = useSDNControllers(clusterId);
  const deleteController = useDeleteSDNController(clusterId);

  if (isLoading) {
    return <Note>Loading...</Note>;
  }

  return (
    <>
      {!controllers || controllers.length === 0 ? (
        <Note>No SDN controllers configured.</Note>
      ) : (
        <div>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Controller</TableHead>
                <TableHead>Type</TableHead>
                <TableHead>ASN</TableHead>
                <TableHead>Peers</TableHead>
                <TableHead>Nodes</TableHead>
                <TableHead className="w-0">
                  <span className="sr-only">Actions</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {controllers.map((ctrl) => (
                <TableRow key={ctrl.controller}>
                  <TableCell className="font-medium">
                    {ctrl.controller}
                    <ClusterHistoryButton
                      history={clusterHistories.sdnController(
                        clusterId,
                        ctrl.controller,
                      )}
                      title={ctrl.controller}
                    />
                  </TableCell>
                  <TableCell>{ctrl.type}</TableCell>
                  <TableCell>{ctrl.asn ?? "-"}</TableCell>
                  <TableCell>{ctrl.peers || "-"}</TableCell>
                  <TableCell>{ctrl.nodes || "-"}</TableCell>
                  <TableCell className="py-1 text-right">
                    <div className="flex justify-end gap-1">
                      <CreateSDNControllerDialog
                        clusterId={clusterId}
                        initialData={ctrl}
                      />
                      <DeleteConfirmButton
                        name={ctrl.controller}
                        kind="Controller"
                        onConfirm={() => {
                          deleteController.mutate(ctrl.controller);
                        }}
                        isPending={deleteController.isPending}
                      />
                    </div>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
    </>
  );
}

function IPAMTab({ clusterId }: { clusterId: string }) {
  const { data: ipams, isLoading } = useSDNIPAMs(clusterId);
  const deleteIPAM = useDeleteSDNIPAM(clusterId);

  if (isLoading) {
    return <Note>Loading...</Note>;
  }

  return (
    <>
      {!ipams || ipams.length === 0 ? (
        <Note>No IPAM plugins configured.</Note>
      ) : (
        <div>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>IPAM</TableHead>
                <TableHead>Type</TableHead>
                <TableHead>URL</TableHead>
                <TableHead className="w-0">
                  <span className="sr-only">Actions</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {ipams.map((ipam) => (
                <TableRow key={ipam.ipam}>
                  <TableCell className="font-medium">
                    {ipam.ipam}
                    <ClusterHistoryButton
                      history={clusterHistories.sdnIpam(clusterId, ipam.ipam)}
                      title={ipam.ipam}
                    />
                  </TableCell>
                  <TableCell>{ipam.type}</TableCell>
                  <TableCell>{ipam.url || "-"}</TableCell>
                  <TableCell className="py-1 text-right">
                    <div className="flex justify-end gap-1">
                      <CreateSDNIPAMDialog
                        clusterId={clusterId}
                        initialData={ipam}
                      />
                      <DeleteConfirmButton
                        name={ipam.ipam}
                        kind="IPAM"
                        onConfirm={() => {
                          deleteIPAM.mutate(ipam.ipam);
                        }}
                        isPending={deleteIPAM.isPending}
                      />
                    </div>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
    </>
  );
}

function DNSTab({ clusterId }: { clusterId: string }) {
  const { data: dnsPlugins, isLoading } = useSDNDNSPlugins(clusterId);
  const deleteDNS = useDeleteSDNDNS(clusterId);

  if (isLoading) {
    return <Note>Loading...</Note>;
  }

  return (
    <>
      {!dnsPlugins || dnsPlugins.length === 0 ? (
        <Note>No DNS plugins configured.</Note>
      ) : (
        <div>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>DNS</TableHead>
                <TableHead>Type</TableHead>
                <TableHead>URL</TableHead>
                <TableHead className="w-0">
                  <span className="sr-only">Actions</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {dnsPlugins.map((dns) => (
                <TableRow key={dns.dns}>
                  <TableCell className="font-medium">
                    {dns.dns}
                    <ClusterHistoryButton
                      history={clusterHistories.sdnDns(clusterId, dns.dns)}
                      title={dns.dns}
                    />
                  </TableCell>
                  <TableCell>{dns.type}</TableCell>
                  <TableCell>{dns.url || "-"}</TableCell>
                  <TableCell className="py-1 text-right">
                    <div className="flex justify-end gap-1">
                      <CreateSDNDNSDialog
                        clusterId={clusterId}
                        initialData={dns}
                      />
                      <DeleteConfirmButton
                        name={dns.dns}
                        kind="DNS"
                        onConfirm={() => {
                          deleteDNS.mutate(dns.dns);
                        }}
                        isPending={deleteDNS.isPending}
                      />
                    </div>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
    </>
  );
}

/** The kinds of SDN object, in the order the switch shows them. */
const SDN_VIEWS = [
  { value: "zones", label: "Zones" },
  { value: "vnets", label: "VNets" },
  { value: "controllers", label: "Controllers" },
  { value: "ipam", label: "IPAM" },
  { value: "dns", label: "DNS" },
] as const;

function countOf(count: number, one: string, many: string): string {
  return `${String(count)} ${count === 1 ? one : many}`;
}

export function SDNTable({ clusterId }: SDNTableProps) {
  const { data: zones, isLoading: zonesLoading } = useSDNZones(clusterId);
  const { data: vnets, isLoading: vnetsLoading } = useSDNVNets(clusterId);
  const deleteZone = useDeleteSDNZone(clusterId);
  const applySDN = useApplySDN(clusterId);
  const [applyOpen, setApplyOpen] = useState(false);

  // The panels are Radix tab panels, so only the kind on show is mounted and
  // reads its data; the switch in the panel picks it.
  const [view, setView] = useState("zones");

  if (zonesLoading || vnetsLoading) {
    return (
      <Panel title="SDN" aside="Loading...">
        <Skeleton className="h-24 w-full" />
      </Panel>
    );
  }

  return (
    <>
      <Tabs value={view} onValueChange={setView}>
        <Panel
          flush
          title="SDN"
          aside={`${countOf(zones?.length ?? 0, "zone", "zones")}, ${countOf(vnets?.length ?? 0, "VNet", "VNets")}. A change reaches the nodes when it is applied.`}
          actions={
            <>
              <Button
                variant="outline"
                size="sm"
                onClick={() => {
                  setApplyOpen(true);
                }}
                disabled={applySDN.isPending}
              >
                <RefreshCw className="h-4 w-4" />
                Apply Changes
              </Button>
              {view === "zones" && (
                <CreateSDNZoneDialog clusterId={clusterId} />
              )}
              {view === "vnets" && (
                <CreateSDNVNetDialog clusterId={clusterId} />
              )}
              {view === "controllers" && (
                <CreateSDNControllerDialog clusterId={clusterId} />
              )}
              {view === "ipam" && <CreateSDNIPAMDialog clusterId={clusterId} />}
              {view === "dns" && <CreateSDNDNSDialog clusterId={clusterId} />}
            </>
          }
        >
          <div className="px-[18px] pb-3">
            <Segmented
              aria-label="SDN objects"
              value={view}
              onValueChange={setView}
              options={SDN_VIEWS}
            />
          </div>

          {/* Each panel is named by its kind: a dialog whose opener is gone
              when it closes (a deleted row) sends focus to the panel. */}
          <TabsContent
            value="zones"
            aria-label="Zones"
            aria-labelledby={undefined}
            className="mt-0"
          >
            {!zones || zones.length === 0 ? (
              <Note>
                No SDN zones configured. SDN may not be enabled on this cluster.
              </Note>
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Zone</TableHead>
                    <TableHead>Type</TableHead>
                    <TableHead>Nodes</TableHead>
                    <TableHead>Bridge</TableHead>
                    <TableHead>MTU</TableHead>
                    <TableHead className="w-0">
                      <span className="sr-only">Actions</span>
                    </TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {zones.map((zone) => (
                    <TableRow key={zone.zone}>
                      <TableCell className="font-medium">
                        {zone.zone}
                        <ClusterHistoryButton
                          history={clusterHistories.sdnZone(
                            clusterId,
                            zone.zone,
                          )}
                          title={zone.zone}
                        />
                      </TableCell>
                      <TableCell>{zone.type}</TableCell>
                      <TableCell>{zone.nodes || "All"}</TableCell>
                      <TableCell>{zone.bridge || "-"}</TableCell>
                      <TableCell>{zone.mtu || "-"}</TableCell>
                      <TableCell className="py-1 text-right">
                        <div className="flex justify-end gap-1">
                          <CreateSDNZoneDialog
                            clusterId={clusterId}
                            initialData={zone}
                          />
                          <DeleteConfirmButton
                            name={zone.zone}
                            kind="Zone"
                            onConfirm={() => {
                              deleteZone.mutate(zone.zone);
                            }}
                            isPending={deleteZone.isPending}
                          />
                        </div>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
          </TabsContent>

          <TabsContent
            value="vnets"
            aria-label="VNets"
            aria-labelledby={undefined}
            className="mt-0"
          >
            {!vnets || vnets.length === 0 ? (
              <Note>No VNets configured.</Note>
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead className="w-8" />
                    <TableHead>VNet</TableHead>
                    <TableHead>Zone</TableHead>
                    <TableHead>VLAN tag</TableHead>
                    <TableHead>Alias</TableHead>
                    <TableHead className="w-0">
                      <span className="sr-only">Actions</span>
                    </TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {vnets.map((vnet) => (
                    <VNetSubnetsRow
                      key={vnet.vnet}
                      clusterId={clusterId}
                      vnet={vnet}
                    />
                  ))}
                </TableBody>
              </Table>
            )}
          </TabsContent>

          <TabsContent
            value="controllers"
            aria-label="Controllers"
            aria-labelledby={undefined}
            className="mt-0"
          >
            <ControllersTab clusterId={clusterId} />
          </TabsContent>

          <TabsContent
            value="ipam"
            aria-label="IPAM"
            aria-labelledby={undefined}
            className="mt-0"
          >
            <IPAMTab clusterId={clusterId} />
          </TabsContent>

          <TabsContent
            value="dns"
            aria-label="DNS"
            aria-labelledby={undefined}
            className="mt-0"
          >
            <DNSTab clusterId={clusterId} />
          </TabsContent>
        </Panel>
      </Tabs>

      <Dialog open={applyOpen} onOpenChange={setApplyOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Apply SDN changes</DialogTitle>
            <DialogDescription>
              This will apply all pending SDN configuration changes to the
              cluster. Are you sure?
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button
              variant="outline"
              onClick={() => {
                setApplyOpen(false);
              }}
            >
              Cancel
            </Button>
            <Button
              onClick={() => {
                applySDN.mutate(undefined, {
                  onSuccess: () => {
                    setApplyOpen(false);
                  },
                });
              }}
              disabled={applySDN.isPending}
            >
              Apply
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}
