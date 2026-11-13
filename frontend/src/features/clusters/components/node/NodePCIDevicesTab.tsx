import { Panel } from "@/components/page/Panel";
import { QueryStateNotice } from "@/components/QueryStateNotice";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { useNodePCIDevices } from "../../api/cluster-queries";
import { NoValue } from "./StateText";

export function PCIDevicesTab({
  clusterId,
  nodeId,
}: {
  clusterId: string;
  nodeId: string;
}) {
  const pciQuery = useNodePCIDevices(clusterId, nodeId);
  const pciDevices = pciQuery.data;
  const count = pciDevices?.length ?? 0;

  // The panel and its title are outside the branch, as in ServicesTab: a
  // failure has to read as this section failing, not as the section not being
  // there.
  return (
    <Panel
      flush
      title="PCI devices"
      aside={
        count > 0
          ? `${String(count)} ${count === 1 ? "device" : "devices"}`
          : undefined
      }
    >
      {!pciDevices || pciDevices.length === 0 ? (
        <div className="px-[18px] pb-1">
          <QueryStateNotice
            query={pciQuery}
            subject="this node's PCI devices"
            empty="No PCI devices found."
            skeletonClassName="h-48 w-full"
          />
        </div>
      ) : (
        <Table className="whitespace-nowrap">
          <TableHeader>
            <TableRow>
              <TableHead>PCI ID</TableHead>
              <TableHead>Device</TableHead>
              <TableHead>Vendor</TableHead>
              <TableHead>IOMMU group</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {pciDevices.map((d) => (
              <TableRow key={d.id}>
                <TableCell className="whitespace-nowrap font-mono text-[13px]">
                  {d.pci_id}
                </TableCell>
                <TableCell>
                  {d.device_name || d.device || <NoValue />}
                </TableCell>
                <TableCell className="text-muted-foreground">
                  {d.vendor_name || d.vendor || <NoValue />}
                </TableCell>
                <TableCell className="tabular-nums">
                  {d.iommu_group >= 0 ? String(d.iommu_group) : <NoValue />}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </Panel>
  );
}
