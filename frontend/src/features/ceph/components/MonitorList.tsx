import { Panel } from "@/components/page/Panel";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { ClusterHistoryButton } from "@/features/history/components/ClusterHistoryButton";
import { clusterHistories } from "@/features/history/lib/cluster-histories";
import type { CephMon } from "../types/ceph";

interface MonitorListProps {
  monitors: CephMon[];
  clusterId: string;
}

export function MonitorList({ monitors, clusterId }: MonitorListProps) {
  return (
    <Panel
      flush
      title="Monitors"
      aside={
        monitors.length === 0
          ? undefined
          : monitors.length === 1
            ? "1 monitor"
            : `${String(monitors.length)} monitors`
      }
    >
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Name</TableHead>
            <TableHead>Host</TableHead>
            <TableHead>Address</TableHead>
            <TableHead className="text-right">Rank</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {monitors.map((mon) => (
            <TableRow key={mon.name}>
              <TableCell>
                <div className="flex items-center gap-2">
                  <span className="font-medium">{mon.name}</span>
                  <ClusterHistoryButton
                    history={clusterHistories.cephMonitor(clusterId, mon.name)}
                    title={mon.name}
                  />
                </div>
              </TableCell>
              <TableCell>{mon.host}</TableCell>
              <TableCell className="font-mono text-[12.5px]">
                {mon.addr}
              </TableCell>
              <TableCell className="text-right font-mono tabular-nums">
                {mon.rank}
              </TableCell>
            </TableRow>
          ))}
          {monitors.length === 0 && (
            <TableRow className="hover:bg-transparent">
              <TableCell
                colSpan={4}
                className="py-8 text-center text-muted-foreground"
              >
                No monitors found.
              </TableCell>
            </TableRow>
          )}
        </TableBody>
      </Table>
    </Panel>
  );
}
