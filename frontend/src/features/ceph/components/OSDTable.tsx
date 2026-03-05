import { Badge } from "@/components/ui/badge";
import type { CephOSD } from "../types/ceph";

interface OSDTableProps {
  osds: CephOSD[];
}

export function OSDTable({ osds }: OSDTableProps) {
  const sorted = [...osds].sort((a, b) => a.id - b.id);

  return (
    <div className="overflow-x-auto rounded-md border">
      <table className="w-full text-sm">
        <thead>
          <tr className="border-b bg-muted/50">
            <th className="px-4 py-2 text-left font-medium">ID</th>
            <th className="px-4 py-2 text-left font-medium">Name</th>
            <th className="px-4 py-2 text-left font-medium">Host</th>
            <th className="px-4 py-2 text-left font-medium">Status</th>
            <th className="px-4 py-2 text-right font-medium">CRUSH Weight</th>
          </tr>
        </thead>
        <tbody>
          {sorted.map((osd) => (
            <tr key={osd.id} className="border-b last:border-0">
              <td className="px-4 py-2 font-mono">{osd.id}</td>
              <td className="px-4 py-2">{osd.name}</td>
              <td className="px-4 py-2">{osd.host}</td>
              <td className="px-4 py-2">
                <div className="flex gap-1">
                  <Badge variant={osd.up === 1 ? "default" : "destructive"}>
                    {osd.up === 1 ? "Up" : "Down"}
                  </Badge>
                  <Badge variant={osd.in === 1 ? "default" : "secondary"}>
                    {osd.in === 1 ? "In" : "Out"}
                  </Badge>
                </div>
              </td>
              <td className="px-4 py-2 text-right font-mono">
                {osd.crush_weight.toFixed(4)}
              </td>
            </tr>
          ))}
          {sorted.length === 0 && (
            <tr>
              <td
                colSpan={5}
                className="px-4 py-8 text-center text-muted-foreground"
              >
                No OSDs found.
              </td>
            </tr>
          )}
        </tbody>
      </table>
    </div>
  );
}
