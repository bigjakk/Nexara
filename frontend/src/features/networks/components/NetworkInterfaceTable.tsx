import { Fragment, useState } from "react";
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
import { Panel } from "@/components/page/Panel";
import { Skeleton } from "@/components/ui/skeleton";
import { Eye, Trash2, Pencil } from "lucide-react";
import { usePermissions } from "@/hooks/usePermissions";
import {
  useNetworkInterfaces,
  useDeleteNetworkInterface,
} from "../api/network-queries";
import type { NetworkInterface } from "../types/network";
import { ClusterHistoryButton } from "@/features/history/components/ClusterHistoryButton";
import { clusterHistories } from "@/features/history/lib/cluster-histories";
import { CreateInterfaceDialog } from "./CreateInterfaceDialog";
import { InterfaceFormDialog } from "./InterfaceFormDialog";
import { NetworkApplyRevertButtons } from "./NetworkApplyRevertButtons";
import { ConfirmInterfaceDeleteDialog } from "./ConfirmInterfaceDeleteDialog";
import {
  interfaceAddresses,
  interfaceGateways,
  interfacePorts,
  interfaceTypeLabel,
} from "./interface-fields";

interface NetworkInterfaceTableProps {
  clusterId: string;
}

/** The native node filter, sized to sit in a panel's title row. */
const FILTER_CLASS =
  "h-8 max-w-full rounded-md border border-input bg-transparent px-2.5 text-sm outline-hidden focus-visible:ring-2 focus-visible:ring-ring";

export function NetworkInterfaceTable({
  clusterId,
}: NetworkInterfaceTableProps) {
  const { data: nodeInterfaces, isLoading } = useNetworkInterfaces(clusterId);
  const [selectedNode, setSelectedNode] = useState("");
  // An interface's create, update, apply and revert routes take
  // manage:network on the cluster; its delete takes delete:network.
  const { canManage, canDelete } = usePermissions();
  const canEdit = canManage("network");
  const canRemove = canDelete("network");

  if (isLoading) {
    return (
      <Panel title="Interfaces" aside="Loading...">
        <Skeleton className="h-24 w-full" />
      </Panel>
    );
  }

  if (!nodeInterfaces || nodeInterfaces.length === 0) {
    return (
      <Panel title="Interfaces">
        <p className="text-sm text-muted-foreground">
          No network interfaces found.
        </p>
      </Panel>
    );
  }

  const nodes = nodeInterfaces.map((ni) => ni.node);
  const filteredInterfaces = selectedNode
    ? nodeInterfaces.filter((ni) => ni.node === selectedNode)
    : nodeInterfaces;
  const shownCount = filteredInterfaces.reduce(
    (sum, ni) => sum + ni.interfaces.length,
    0,
  );

  return (
    <Panel
      flush
      title="Interfaces"
      aside={
        selectedNode
          ? `${String(shownCount)} on ${selectedNode}`
          : `${String(shownCount)} on ${String(nodes.length)} ${nodes.length === 1 ? "node" : "nodes"}${canEdit ? ". Pick a node to add an interface or apply its pending changes." : ""}`
      }
      actions={
        <>
          <select
            aria-label="Filter by node"
            className={FILTER_CLASS}
            value={selectedNode}
            onChange={(e) => {
              setSelectedNode(e.target.value);
            }}
          >
            <option value="">All nodes</option>
            {nodes.map((node) => (
              <option key={node} value={node}>
                {node}
              </option>
            ))}
          </select>
          {selectedNode && (
            <>
              <NetworkApplyRevertButtons
                clusterId={clusterId}
                nodeName={selectedNode}
              />
              <CreateInterfaceDialog
                clusterId={clusterId}
                nodeName={selectedNode}
              />
            </>
          )}
        </>
      }
    >
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Interface</TableHead>
            <TableHead>Type</TableHead>
            <TableHead>Address</TableHead>
            <TableHead>Gateway</TableHead>
            <TableHead>State</TableHead>
            <TableHead>Ports</TableHead>
            <TableHead>Comment</TableHead>
            <TableHead className="w-0">
              <span className="sr-only">Actions</span>
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {filteredInterfaces.map((ni) => (
            <Fragment key={ni.node}>
              {/* One band per node: the node is the group, not a column
                  repeated on every row. */}
              <TableRow className="bg-foreground/[0.03] hover:bg-foreground/[0.03]">
                <TableCell colSpan={8} className="py-1.5">
                  <span className="font-semibold">{ni.node}</span>
                  <span className="ml-2 text-[12.5px] text-muted-foreground">
                    {`${String(ni.interfaces.length)} ${ni.interfaces.length === 1 ? "interface" : "interfaces"}`}
                  </span>
                </TableCell>
              </TableRow>
              {ni.interfaces.map((iface: NetworkInterface) => (
                <InterfaceRow
                  key={`${ni.node}-${iface.iface}`}
                  nodeName={ni.node}
                  iface={iface}
                  clusterId={clusterId}
                  canEdit={canEdit}
                  canRemove={canRemove}
                />
              ))}
            </Fragment>
          ))}
        </TableBody>
      </Table>
    </Panel>
  );
}

/** What a bridge or bond adds to its ports: VLAN awareness, the bond mode. */
function portsNote(iface: NetworkInterface): string {
  const notes = [
    ...(iface.type === "bridge" && iface.bridge_vlan_aware
      ? ["VLAN aware"]
      : []),
    ...(iface.bond_mode ? [iface.bond_mode] : []),
  ];
  return notes.join(", ");
}

function InterfaceRow({
  nodeName,
  iface,
  clusterId,
  canEdit,
  canRemove,
}: {
  nodeName: string;
  iface: NetworkInterface;
  clusterId: string;
  canEdit: boolean;
  canRemove: boolean;
}) {
  const deleteIface = useDeleteNetworkInterface(clusterId, nodeName);
  const [editing, setEditing] = useState(false);
  const [pendingDelete, setPendingDelete] = useState<NetworkInterface | null>(
    null,
  );
  const note = portsNote(iface);

  return (
    <TableRow>
      <TableCell className="whitespace-nowrap pl-9 font-mono text-[12.5px] font-medium">
        {iface.iface}
        <ClusterHistoryButton
          history={clusterHistories.networkInterface(
            clusterId,
            nodeName,
            iface.iface,
          )}
          title={`${iface.iface} on ${nodeName}`}
        />
      </TableCell>
      <TableCell className="whitespace-nowrap">
        {interfaceTypeLabel(iface.type)}
      </TableCell>
      <TableCell className="font-mono text-[12.5px]">
        {interfaceAddresses(iface)}
      </TableCell>
      <TableCell className="font-mono text-[12.5px]">
        {interfaceGateways(iface)}
      </TableCell>
      <TableCell>
        <Badge variant={iface.active ? "healthy" : "secondary"}>
          {iface.active ? "Active" : "Inactive"}
        </Badge>
      </TableCell>
      <TableCell>
        <span className="font-mono text-[12.5px]">{interfacePorts(iface)}</span>
        {note !== "" && (
          <span className="block text-[12.5px] text-muted-foreground">
            {note}
          </span>
        )}
      </TableCell>
      <TableCell className="text-muted-foreground">
        {iface.comments ?? "--"}
      </TableCell>
      <TableCell className="py-1">
        <div className="flex justify-end gap-1">
          {/* Without manage:network the same button opens the form
              read-only: it is the one place an interface's settings show. */}
          <Button
            aria-label={`${canEdit ? "Edit" : "View"} ${iface.iface} on ${nodeName}`}
            variant="ghost"
            size="icon"
            className="h-8 w-8 text-muted-foreground"
            onClick={() => {
              setEditing(true);
            }}
          >
            {canEdit ? (
              <Pencil className="h-4 w-4" />
            ) : (
              <Eye className="h-4 w-4" />
            )}
          </Button>
          {canRemove && (
            <Button
              aria-label={`Delete ${iface.iface} on ${nodeName}`}
              variant="ghost"
              size="icon"
              className="h-8 w-8 text-muted-foreground hover:text-destructive"
              onClick={() => {
                setPendingDelete(iface);
              }}
              disabled={deleteIface.isPending}
            >
              <Trash2 className="h-4 w-4" />
            </Button>
          )}
        </div>
        {editing && (
          <InterfaceFormDialog
            clusterId={clusterId}
            nodeName={nodeName}
            existing={iface}
            readOnly={!canEdit}
            open
            onOpenChange={(open) => {
              if (!open) setEditing(false);
            }}
          />
        )}
        <ConfirmInterfaceDeleteDialog
          target={pendingDelete}
          nodeName={nodeName}
          onClose={() => {
            setPendingDelete(null);
          }}
          onConfirm={(target) => {
            deleteIface.mutate(target.iface);
          }}
        />
      </TableCell>
    </TableRow>
  );
}
