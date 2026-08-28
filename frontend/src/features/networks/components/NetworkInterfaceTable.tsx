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
import { Trash2, RotateCcw, Check, Pencil } from "lucide-react";
import {
  useNetworkInterfaces,
  useDeleteNetworkInterface,
  useApplyNetworkConfig,
  useRevertNetworkConfig,
} from "../api/network-queries";
import type { NetworkInterface } from "../types/network";
import { CreateInterfaceDialog } from "./CreateInterfaceDialog";
import { InterfaceFormDialog } from "./InterfaceFormDialog";
import {
  interfaceAddresses,
  interfaceGateways,
  interfacePorts,
  interfaceTypeLabel,
} from "./interface-fields";

interface NetworkInterfaceTableProps {
  clusterId: string;
}

export function NetworkInterfaceTable({
  clusterId,
}: NetworkInterfaceTableProps) {
  const { data: nodeInterfaces, isLoading } = useNetworkInterfaces(clusterId);
  const [selectedNode, setSelectedNode] = useState("");

  if (isLoading) {
    return <p className="text-sm text-muted-foreground">Loading...</p>;
  }

  if (!nodeInterfaces || nodeInterfaces.length === 0) {
    return (
      <p className="text-sm text-muted-foreground">
        No network interfaces found.
      </p>
    );
  }

  const nodes = nodeInterfaces.map((ni) => ni.node);
  const filteredInterfaces = selectedNode
    ? nodeInterfaces.filter((ni) => ni.node === selectedNode)
    : nodeInterfaces;

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2">
          <span className="text-sm font-medium">Filter by node:</span>
          <select
            className="rounded-md border bg-background px-3 py-1.5 text-sm"
            value={selectedNode}
            onChange={(e) => { setSelectedNode(e.target.value); }}
          >
            <option value="">All nodes</option>
            {nodes.map((node) => (
              <option key={node} value={node}>
                {node}
              </option>
            ))}
          </select>
        </div>
        <div className="flex items-center gap-2">
          {selectedNode && (
            <>
              <ApplyRevertButtons
                clusterId={clusterId}
                nodeName={selectedNode}
              />
              <CreateInterfaceDialog
                clusterId={clusterId}
                nodeName={selectedNode}
              />
            </>
          )}
        </div>
      </div>

      <div className="rounded-md border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Node</TableHead>
              <TableHead>Interface</TableHead>
              <TableHead>Type</TableHead>
              <TableHead>CIDR / Address</TableHead>
              <TableHead>Gateway</TableHead>
              <TableHead>Status</TableHead>
              <TableHead>VLAN aware</TableHead>
              <TableHead>Ports / Slaves</TableHead>
              <TableHead>Bond Mode</TableHead>
              <TableHead>Comment</TableHead>
              <TableHead className="w-24" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {filteredInterfaces.flatMap((ni) =>
              ni.interfaces.map((iface: NetworkInterface) => (
                <InterfaceRow
                  key={`${ni.node}-${iface.iface}`}
                  nodeName={ni.node}
                  iface={iface}
                  clusterId={clusterId}
                />
              )),
            )}
          </TableBody>
        </Table>
      </div>
    </div>
  );
}

function InterfaceRow({
  nodeName,
  iface,
  clusterId,
}: {
  nodeName: string;
  iface: NetworkInterface;
  clusterId: string;
}) {
  const deleteIface = useDeleteNetworkInterface(clusterId, nodeName);
  const [editing, setEditing] = useState(false);

  return (
    <TableRow>
      <TableCell className="font-medium">{nodeName}</TableCell>
      <TableCell className="font-mono text-sm">{iface.iface}</TableCell>
      <TableCell>
        <Badge variant="outline">{interfaceTypeLabel(iface.type)}</Badge>
      </TableCell>
      <TableCell className="font-mono text-sm">
        {interfaceAddresses(iface)}
      </TableCell>
      <TableCell className="font-mono text-sm">
        {interfaceGateways(iface)}
      </TableCell>
      <TableCell>
        <Badge variant={iface.active ? "default" : "secondary"}>
          {iface.active ? "Active" : "Inactive"}
        </Badge>
      </TableCell>
      <TableCell className="text-sm">
        {iface.type === "bridge" ? (iface.bridge_vlan_aware ? "Yes" : "No") : "--"}
      </TableCell>
      <TableCell className="font-mono text-sm">{interfacePorts(iface)}</TableCell>
      <TableCell className="text-sm">{iface.bond_mode ?? "--"}</TableCell>
      <TableCell className="text-sm">{iface.comments ?? "--"}</TableCell>
      <TableCell>
        <div className="flex gap-1">
          <Button
            variant="ghost"
            size="icon"
            onClick={() => { setEditing(true); }}
          >
            <Pencil className="h-4 w-4" />
          </Button>
          <Button
            variant="ghost"
            size="icon"
            onClick={() => { deleteIface.mutate(iface.iface); }}
            disabled={deleteIface.isPending}
          >
            <Trash2 className="h-4 w-4 text-destructive" />
          </Button>
        </div>
        {editing && (
          <InterfaceFormDialog
            clusterId={clusterId}
            nodeName={nodeName}
            existing={iface}
            open
            onOpenChange={(open) => { if (!open) setEditing(false); }}
          />
        )}
      </TableCell>
    </TableRow>
  );
}

function ApplyRevertButtons({
  clusterId,
  nodeName,
}: {
  clusterId: string;
  nodeName: string;
}) {
  const apply = useApplyNetworkConfig(clusterId, nodeName);
  const revert = useRevertNetworkConfig(clusterId, nodeName);

  return (
    <>
      <Button
        variant="outline"
        size="sm"
        onClick={() => { revert.mutate(); }}
        disabled={revert.isPending}
      >
        <RotateCcw className="mr-1 h-4 w-4" />
        Revert
      </Button>
      <Button
        size="sm"
        onClick={() => { apply.mutate(); }}
        disabled={apply.isPending}
      >
        <Check className="mr-1 h-4 w-4" />
        Apply
      </Button>
    </>
  );
}
