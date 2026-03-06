import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";
import { useSDNZones, useSDNVNets } from "../api/network-queries";

interface SDNTableProps {
  clusterId: string;
}

export function SDNTable({ clusterId }: SDNTableProps) {
  const { data: zones, isLoading: zonesLoading } = useSDNZones(clusterId);
  const { data: vnets, isLoading: vnetsLoading } = useSDNVNets(clusterId);

  if (zonesLoading || vnetsLoading) {
    return <p className="text-sm text-muted-foreground">Loading...</p>;
  }

  return (
    <div className="space-y-6">
      <div>
        <h3 className="mb-2 text-lg font-medium">SDN Zones</h3>
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
                  <TableHead>IPAM</TableHead>
                  <TableHead>DNS Zone</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {zones.map((zone) => (
                  <TableRow key={zone.zone}>
                    <TableCell className="font-medium">{zone.zone}</TableCell>
                    <TableCell>
                      <Badge variant="outline">{zone.type}</Badge>
                    </TableCell>
                    <TableCell>{zone.nodes || "All"}</TableCell>
                    <TableCell>{zone.ipam || "-"}</TableCell>
                    <TableCell>{zone.dnszone || "-"}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
      </div>

      <div>
        <h3 className="mb-2 text-lg font-medium">VNets</h3>
        {!vnets || vnets.length === 0 ? (
          <p className="text-sm text-muted-foreground">No VNets configured.</p>
        ) : (
          <div className="rounded-md border">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>VNet</TableHead>
                  <TableHead>Zone</TableHead>
                  <TableHead>VLAN Tag</TableHead>
                  <TableHead>Alias</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {vnets.map((vnet) => (
                  <TableRow key={vnet.vnet}>
                    <TableCell className="font-medium">{vnet.vnet}</TableCell>
                    <TableCell>{vnet.zone}</TableCell>
                    <TableCell>{vnet.tag ?? "-"}</TableCell>
                    <TableCell>{vnet.alias || "-"}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
      </div>
    </div>
  );
}
