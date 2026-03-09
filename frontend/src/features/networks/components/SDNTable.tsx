import { useState } from "react";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
} from "@/components/ui/dialog";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
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
import { CreateSDNZoneDialog } from "./CreateSDNZoneDialog";
import { CreateSDNVNetDialog } from "./CreateSDNVNetDialog";
import { CreateSDNSubnetDialog } from "./CreateSDNSubnetDialog";
import { CreateSDNControllerDialog } from "./CreateSDNControllerDialog";
import { CreateSDNIPAMDialog } from "./CreateSDNIPAMDialog";
import { CreateSDNDNSDialog } from "./CreateSDNDNSDialog";
import type { SDNVNet } from "../types/network";

interface SDNTableProps {
  clusterId: string;
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
        variant="ghost"
        size="icon"
        onClick={() => { setOpen(true); }}
      >
        <Trash2 className="h-4 w-4 text-destructive" />
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
          <div className="flex justify-end gap-2">
            <Button variant="outline" onClick={() => { setOpen(false); }}>
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
          </div>
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

  return (
    <>
      <TableRow
        className="cursor-pointer"
        onClick={() => { setExpanded(!expanded); }}
      >
        <TableCell className="w-8">
          {expanded ? (
            <ChevronDown className="h-4 w-4" />
          ) : (
            <ChevronRight className="h-4 w-4" />
          )}
        </TableCell>
        <TableCell className="font-medium">{vnet.vnet}</TableCell>
        <TableCell>{vnet.zone}</TableCell>
        <TableCell>{vnet.tag ?? "-"}</TableCell>
        <TableCell>{vnet.alias || "-"}</TableCell>
        <TableCell
          className="text-right"
          onClick={(e) => { e.stopPropagation(); }}
        >
          <div className="flex justify-end gap-1">
            <CreateSDNVNetDialog clusterId={clusterId} initialData={vnet} />
            <DeleteConfirmButton
              name={vnet.vnet}
              kind="VNet"
              onConfirm={() => { deleteVNet.mutate(vnet.vnet); }}
              isPending={deleteVNet.isPending}
            />
          </div>
        </TableCell>
      </TableRow>
      {expanded && (
        <TableRow>
          <TableCell colSpan={6} className="bg-muted/50 p-4">
            <div className="space-y-2">
              <div className="flex items-center justify-between">
                <span className="text-sm font-medium">Subnets</span>
                <CreateSDNSubnetDialog
                  clusterId={clusterId}
                  vnet={vnet.vnet}
                />
              </div>
              {isLoading ? (
                <p className="text-sm text-muted-foreground">Loading...</p>
              ) : !subnets || subnets.length === 0 ? (
                <p className="text-sm text-muted-foreground">
                  No subnets configured.
                </p>
              ) : (
                <div className="rounded-md border">
                  <Table>
                    <TableHeader>
                      <TableRow>
                        <TableHead>Subnet</TableHead>
                        <TableHead>Gateway</TableHead>
                        <TableHead>SNAT</TableHead>
                        <TableHead className="text-right">Actions</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {subnets.map((s) => (
                        <TableRow key={s.subnet}>
                          <TableCell className="font-medium">
                            {s.subnet}
                          </TableCell>
                          <TableCell>{s.gateway || "-"}</TableCell>
                          <TableCell>
                            {s.snat === 1 ? (
                              <Badge variant="default">Yes</Badge>
                            ) : (
                              "No"
                            )}
                          </TableCell>
                          <TableCell className="text-right">
                            <div className="flex justify-end gap-1">
                              <CreateSDNSubnetDialog
                                clusterId={clusterId}
                                vnet={vnet.vnet}
                                initialData={s}
                              />
                              <Button
                                variant="ghost"
                                size="icon"
                                onClick={() => {
                                  deleteSubnet.mutate(s.subnet);
                                }}
                                disabled={deleteSubnet.isPending}
                              >
                                <Trash2 className="h-4 w-4 text-destructive" />
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
    return <p className="text-sm text-muted-foreground">Loading...</p>;
  }

  return (
    <div className="space-y-4">
      <div className="flex justify-end">
        <CreateSDNControllerDialog clusterId={clusterId} />
      </div>
      {!controllers || controllers.length === 0 ? (
        <p className="text-sm text-muted-foreground">
          No SDN controllers configured.
        </p>
      ) : (
        <div className="rounded-md border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Controller</TableHead>
                <TableHead>Type</TableHead>
                <TableHead>ASN</TableHead>
                <TableHead>Peers</TableHead>
                <TableHead>Nodes</TableHead>
                <TableHead className="text-right">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {controllers.map((ctrl) => (
                <TableRow key={ctrl.controller}>
                  <TableCell className="font-medium">
                    {ctrl.controller}
                  </TableCell>
                  <TableCell>
                    <Badge variant="outline">{ctrl.type}</Badge>
                  </TableCell>
                  <TableCell>{ctrl.asn ?? "-"}</TableCell>
                  <TableCell>{ctrl.peers || "-"}</TableCell>
                  <TableCell>{ctrl.nodes || "-"}</TableCell>
                  <TableCell className="text-right">
                    <div className="flex justify-end gap-1">
                      <CreateSDNControllerDialog
                        clusterId={clusterId}
                        initialData={ctrl}
                      />
                      <DeleteConfirmButton
                        name={ctrl.controller}
                        kind="Controller"
                        onConfirm={() => { deleteController.mutate(ctrl.controller); }}
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
    </div>
  );
}

function IPAMTab({ clusterId }: { clusterId: string }) {
  const { data: ipams, isLoading } = useSDNIPAMs(clusterId);
  const deleteIPAM = useDeleteSDNIPAM(clusterId);

  if (isLoading) {
    return <p className="text-sm text-muted-foreground">Loading...</p>;
  }

  return (
    <div className="space-y-4">
      <div className="flex justify-end">
        <CreateSDNIPAMDialog clusterId={clusterId} />
      </div>
      {!ipams || ipams.length === 0 ? (
        <p className="text-sm text-muted-foreground">
          No IPAM plugins configured.
        </p>
      ) : (
        <div className="rounded-md border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>IPAM</TableHead>
                <TableHead>Type</TableHead>
                <TableHead>URL</TableHead>
                <TableHead className="text-right">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {ipams.map((ipam) => (
                <TableRow key={ipam.ipam}>
                  <TableCell className="font-medium">{ipam.ipam}</TableCell>
                  <TableCell>
                    <Badge variant="outline">{ipam.type}</Badge>
                  </TableCell>
                  <TableCell>{ipam.url || "-"}</TableCell>
                  <TableCell className="text-right">
                    <div className="flex justify-end gap-1">
                      <CreateSDNIPAMDialog
                        clusterId={clusterId}
                        initialData={ipam}
                      />
                      <DeleteConfirmButton
                        name={ipam.ipam}
                        kind="IPAM"
                        onConfirm={() => { deleteIPAM.mutate(ipam.ipam); }}
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
    </div>
  );
}

function DNSTab({ clusterId }: { clusterId: string }) {
  const { data: dnsPlugins, isLoading } = useSDNDNSPlugins(clusterId);
  const deleteDNS = useDeleteSDNDNS(clusterId);

  if (isLoading) {
    return <p className="text-sm text-muted-foreground">Loading...</p>;
  }

  return (
    <div className="space-y-4">
      <div className="flex justify-end">
        <CreateSDNDNSDialog clusterId={clusterId} />
      </div>
      {!dnsPlugins || dnsPlugins.length === 0 ? (
        <p className="text-sm text-muted-foreground">
          No DNS plugins configured.
        </p>
      ) : (
        <div className="rounded-md border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>DNS</TableHead>
                <TableHead>Type</TableHead>
                <TableHead>URL</TableHead>
                <TableHead className="text-right">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {dnsPlugins.map((dns) => (
                <TableRow key={dns.dns}>
                  <TableCell className="font-medium">{dns.dns}</TableCell>
                  <TableCell>
                    <Badge variant="outline">{dns.type}</Badge>
                  </TableCell>
                  <TableCell>{dns.url || "-"}</TableCell>
                  <TableCell className="text-right">
                    <div className="flex justify-end gap-1">
                      <CreateSDNDNSDialog
                        clusterId={clusterId}
                        initialData={dns}
                      />
                      <DeleteConfirmButton
                        name={dns.dns}
                        kind="DNS"
                        onConfirm={() => { deleteDNS.mutate(dns.dns); }}
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
    </div>
  );
}

export function SDNTable({ clusterId }: SDNTableProps) {
  const { data: zones, isLoading: zonesLoading } = useSDNZones(clusterId);
  const { data: vnets, isLoading: vnetsLoading } = useSDNVNets(clusterId);
  const deleteZone = useDeleteSDNZone(clusterId);
  const applySDN = useApplySDN(clusterId);
  const [applyOpen, setApplyOpen] = useState(false);

  if (zonesLoading || vnetsLoading) {
    return <p className="text-sm text-muted-foreground">Loading...</p>;
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <div />
        <Button
          variant="outline"
          size="sm"
          onClick={() => { setApplyOpen(true); }}
          disabled={applySDN.isPending}
        >
          <RefreshCw className="mr-1 h-4 w-4" />
          Apply Changes
        </Button>
      </div>

      <Dialog open={applyOpen} onOpenChange={setApplyOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Apply SDN Changes</DialogTitle>
            <DialogDescription>
              This will apply all pending SDN configuration changes to the
              cluster. Are you sure?
            </DialogDescription>
          </DialogHeader>
          <div className="flex justify-end gap-2">
            <Button variant="outline" onClick={() => { setApplyOpen(false); }}>
              Cancel
            </Button>
            <Button
              onClick={() => {
                applySDN.mutate(undefined, {
                  onSuccess: () => { setApplyOpen(false); },
                });
              }}
              disabled={applySDN.isPending}
            >
              Apply
            </Button>
          </div>
        </DialogContent>
      </Dialog>

      <Tabs defaultValue="zones">
        <TabsList>
          <TabsTrigger value="zones">Zones</TabsTrigger>
          <TabsTrigger value="vnets">VNets</TabsTrigger>
          <TabsTrigger value="controllers">Controllers</TabsTrigger>
          <TabsTrigger value="ipam">IPAM</TabsTrigger>
          <TabsTrigger value="dns">DNS</TabsTrigger>
        </TabsList>

        <TabsContent value="zones" className="space-y-4">
          <div className="flex justify-end">
            <CreateSDNZoneDialog clusterId={clusterId} />
          </div>
          {!zones || zones.length === 0 ? (
            <p className="text-sm text-muted-foreground">
              No SDN zones configured. SDN may not be enabled on this cluster.
            </p>
          ) : (
            <div className="rounded-md border">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Zone</TableHead>
                    <TableHead>Type</TableHead>
                    <TableHead>Nodes</TableHead>
                    <TableHead>Bridge</TableHead>
                    <TableHead>MTU</TableHead>
                    <TableHead className="text-right">Actions</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {zones.map((zone) => (
                    <TableRow key={zone.zone}>
                      <TableCell className="font-medium">
                        {zone.zone}
                      </TableCell>
                      <TableCell>
                        <Badge variant="outline">{zone.type}</Badge>
                      </TableCell>
                      <TableCell>{zone.nodes || "All"}</TableCell>
                      <TableCell>{zone.bridge || "-"}</TableCell>
                      <TableCell>{zone.mtu || "-"}</TableCell>
                      <TableCell className="text-right">
                        <div className="flex justify-end gap-1">
                          <CreateSDNZoneDialog
                            clusterId={clusterId}
                            initialData={zone}
                          />
                          <DeleteConfirmButton
                            name={zone.zone}
                            kind="Zone"
                            onConfirm={() => { deleteZone.mutate(zone.zone); }}
                            isPending={deleteZone.isPending}
                          />
                        </div>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          )}
        </TabsContent>

        <TabsContent value="vnets" className="space-y-4">
          <div className="flex justify-end">
            <CreateSDNVNetDialog clusterId={clusterId} />
          </div>
          {!vnets || vnets.length === 0 ? (
            <p className="text-sm text-muted-foreground">
              No VNets configured.
            </p>
          ) : (
            <div className="rounded-md border">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead className="w-8" />
                    <TableHead>VNet</TableHead>
                    <TableHead>Zone</TableHead>
                    <TableHead>VLAN Tag</TableHead>
                    <TableHead>Alias</TableHead>
                    <TableHead className="text-right">Actions</TableHead>
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
            </div>
          )}
        </TabsContent>

        <TabsContent value="controllers" className="space-y-4">
          <ControllersTab clusterId={clusterId} />
        </TabsContent>

        <TabsContent value="ipam" className="space-y-4">
          <IPAMTab clusterId={clusterId} />
        </TabsContent>

        <TabsContent value="dns" className="space-y-4">
          <DNSTab clusterId={clusterId} />
        </TabsContent>
      </Tabs>
    </div>
  );
}
