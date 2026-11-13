import { useState } from "react";
import { Pencil, Trash2 } from "lucide-react";

import { Panel } from "@/components/page/Panel";
import { QueryStateNotice } from "@/components/QueryStateNotice";
import { Button } from "@/components/ui/button";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  useNodeNetworkInterfaces as useNodeNetworkInterfacesLive,
  useDeleteNetworkInterface,
} from "@/features/networks/api/network-queries";
import type { NetworkInterface } from "@/features/networks/types/network";
import { CreateInterfaceDialog } from "@/features/networks/components/CreateInterfaceDialog";
import { NetworkApplyRevertButtons } from "@/features/networks/components/NetworkApplyRevertButtons";
import { InterfaceFormDialog } from "@/features/networks/components/InterfaceFormDialog";
import { ConfirmInterfaceDeleteDialog } from "@/features/networks/components/ConfirmInterfaceDeleteDialog";
import {
  interfaceAddresses,
  interfaceGateways,
  interfacePorts,
  interfaceTypeLabel,
} from "@/features/networks/components/interface-fields";
import { NoValue, StateText } from "./StateText";

/** A cell's text, or the dash when the helper had nothing to say. */
function orDash(value: string | number | undefined) {
  return value === undefined || value === "" || value === "--" ? (
    <NoValue />
  ) : (
    value
  );
}

/** What kind of interface it is, with the settings that qualify the kind. */
function interfaceKind(iface: NetworkInterface): string {
  return [
    interfaceTypeLabel(iface.type),
    iface.bond_mode,
    iface.type === "bridge" && iface.bridge_vlan_aware ? "VLAN aware" : "",
  ]
    .filter((part) => part !== undefined && part !== "")
    .join(" · ");
}

export function NetworkTab({
  clusterId,
  nodeName,
}: {
  clusterId: string;
  nodeName: string;
}) {
  const interfacesQuery = useNodeNetworkInterfacesLive(clusterId, nodeName);
  const networkInterfaces = interfacesQuery.data;
  const deleteIface = useDeleteNetworkInterface(clusterId, nodeName);
  const [editIface, setEditIface] = useState<NetworkInterface | null>(null);
  const [pendingDelete, setPendingDelete] = useState<NetworkInterface | null>(
    null,
  );
  const count = networkInterfaces?.length ?? 0;

  return (
    <>
      <Panel
        flush
        title="Network interfaces"
        aside={
          count > 0
            ? `${String(count)} ${count === 1 ? "interface" : "interfaces"}, ${String(networkInterfaces?.filter((i) => i.active).length ?? 0)} up`
            : undefined
        }
        actions={
          <>
            <NetworkApplyRevertButtons
              clusterId={clusterId}
              nodeName={nodeName}
            />
            <CreateInterfaceDialog clusterId={clusterId} nodeName={nodeName} />
          </>
        }
      >
        {!networkInterfaces || networkInterfaces.length === 0 ? (
          <div className="px-[18px] pb-1">
            <QueryStateNotice
              query={interfacesQuery}
              subject="this node's network interfaces"
              empty="No network interfaces found."
              skeletonClassName="h-48 w-full"
            />
          </div>
        ) : (
          <Table className="whitespace-nowrap">
            <TableHeader>
              <TableRow>
                <TableHead>Interface</TableHead>
                <TableHead>State</TableHead>
                <TableHead>Autostart</TableHead>
                <TableHead>Ports</TableHead>
                <TableHead>Address</TableHead>
                <TableHead>Gateway</TableHead>
                <TableHead>MTU</TableHead>
                <TableHead>Comment</TableHead>
                <TableHead className="w-20" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {networkInterfaces.map((iface) => (
                <TableRow key={iface.iface}>
                  <TableCell>
                    <div className="font-mono text-[13px]">{iface.iface}</div>
                    <div className="text-[12.5px] text-muted-foreground">
                      {interfaceKind(iface)}
                    </div>
                  </TableCell>
                  <TableCell>
                    <StateText tone={iface.active ? "ok" : "off"}>
                      {iface.active ? "Up" : "Down"}
                    </StateText>
                  </TableCell>
                  <TableCell
                    className={
                      iface.autostart ? undefined : "text-muted-foreground"
                    }
                  >
                    {iface.autostart ? "Yes" : "No"}
                  </TableCell>
                  <TableCell className="font-mono text-[12.5px]">
                    {orDash(interfacePorts(iface))}
                  </TableCell>
                  <TableCell className="font-mono text-[12.5px]">
                    {orDash(interfaceAddresses(iface))}
                  </TableCell>
                  <TableCell className="font-mono text-[12.5px]">
                    {orDash(interfaceGateways(iface))}
                  </TableCell>
                  <TableCell className="tabular-nums">
                    {orDash(iface.mtu)}
                  </TableCell>
                  <TableCell className="min-w-[10rem] whitespace-normal text-muted-foreground">
                    {orDash(iface.comments)}
                  </TableCell>
                  <TableCell>
                    <div className="flex justify-end gap-1">
                      <Button
                        aria-label={`Edit ${iface.iface}`}
                        variant="ghost"
                        size="icon"
                        className="h-7 w-7 text-muted-foreground"
                        onClick={() => {
                          setEditIface(iface);
                        }}
                      >
                        <Pencil className="h-3.5 w-3.5" />
                      </Button>
                      <Button
                        aria-label={`Delete ${iface.iface}`}
                        variant="ghost"
                        size="icon"
                        className="h-7 w-7 text-muted-foreground hover:text-destructive"
                        onClick={() => {
                          setPendingDelete(iface);
                        }}
                        disabled={deleteIface.isPending}
                      >
                        <Trash2 className="h-3.5 w-3.5" />
                      </Button>
                    </div>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </Panel>

      {editIface && (
        <InterfaceFormDialog
          clusterId={clusterId}
          nodeName={nodeName}
          existing={editIface}
          open
          onOpenChange={(open) => {
            if (!open) setEditIface(null);
          }}
        />
      )}

      <ConfirmInterfaceDeleteDialog
        target={pendingDelete}
        nodeName={nodeName}
        onClose={() => {
          setPendingDelete(null);
        }}
        onConfirm={(iface) => {
          deleteIface.mutate(iface.iface);
        }}
      />
    </>
  );
}
