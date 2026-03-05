import { Badge } from "@/components/ui/badge";
import type { CephMon } from "../types/ceph";

interface MonitorListProps {
  monitors: CephMon[];
}

export function MonitorList({ monitors }: MonitorListProps) {
  return (
    <div className="overflow-x-auto rounded-md border">
      <table className="w-full text-sm">
        <thead>
          <tr className="border-b bg-muted/50">
            <th className="px-4 py-2 text-left font-medium">Name</th>
            <th className="px-4 py-2 text-left font-medium">Host</th>
            <th className="px-4 py-2 text-left font-medium">Address</th>
            <th className="px-4 py-2 text-right font-medium">Rank</th>
          </tr>
        </thead>
        <tbody>
          {monitors.map((mon) => (
            <tr key={mon.name} className="border-b last:border-0">
              <td className="px-4 py-2">
                <div className="flex items-center gap-2">
                  <Badge variant="default">MON</Badge>
                  <span className="font-medium">{mon.name}</span>
                </div>
              </td>
              <td className="px-4 py-2">{mon.host}</td>
              <td className="px-4 py-2 font-mono text-xs">{mon.addr}</td>
              <td className="px-4 py-2 text-right font-mono">{mon.rank}</td>
            </tr>
          ))}
          {monitors.length === 0 && (
            <tr>
              <td
                colSpan={4}
                className="px-4 py-8 text-center text-muted-foreground"
              >
                No monitors found.
              </td>
            </tr>
          )}
        </tbody>
      </table>
    </div>
  );
}
