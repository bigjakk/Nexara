import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";
import type { PBSSnapshot } from "../types/backup";
import { RestoreDialog } from "./RestoreDialog";

function formatBytes(bytes: number): string {
  if (bytes === 0) return "0 B";
  const k = 1024;
  const sizes = ["B", "KB", "MB", "GB", "TB"];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  return `${(bytes / Math.pow(k, i)).toFixed(1)} ${sizes[i] ?? ""}`;
}

function formatUnixTime(ts: number): string {
  return new Date(ts * 1000).toLocaleString();
}

interface SnapshotTableProps {
  snapshots: PBSSnapshot[];
  pbsId: string;
}

export function SnapshotTable({ snapshots, pbsId }: SnapshotTableProps) {
  if (snapshots.length === 0) {
    return (
      <p className="py-8 text-center text-sm text-muted-foreground">
        No snapshots found.
      </p>
    );
  }

  return (
    <div className="rounded-md border">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Type</TableHead>
            <TableHead>Backup ID</TableHead>
            <TableHead>Datastore</TableHead>
            <TableHead>Time</TableHead>
            <TableHead className="text-right">Size</TableHead>
            <TableHead>Verified</TableHead>
            <TableHead>Protected</TableHead>
            <TableHead>Owner</TableHead>
            <TableHead className="w-12" />
          </TableRow>
        </TableHeader>
        <TableBody>
          {snapshots.map((snap) => (
            <TableRow key={`${snap.backup_type}-${snap.backup_id}-${String(snap.backup_time)}`}>
              <TableCell>
                <Badge variant={snap.backup_type === "vm" ? "default" : "secondary"}>
                  {snap.backup_type.toUpperCase()}
                </Badge>
              </TableCell>
              <TableCell className="font-mono text-sm">
                {snap.backup_id}
              </TableCell>
              <TableCell>{snap.datastore}</TableCell>
              <TableCell className="text-sm">
                {formatUnixTime(snap.backup_time)}
              </TableCell>
              <TableCell className="text-right font-mono text-sm">
                {formatBytes(snap.size)}
              </TableCell>
              <TableCell>
                {snap.verified ? (
                  <Badge variant="default" className="bg-green-600">
                    Yes
                  </Badge>
                ) : (
                  <Badge variant="secondary">No</Badge>
                )}
              </TableCell>
              <TableCell>
                {snap.protected ? (
                  <Badge variant="default">Yes</Badge>
                ) : (
                  <span className="text-muted-foreground">-</span>
                )}
              </TableCell>
              <TableCell className="text-sm text-muted-foreground">
                {snap.owner || "-"}
              </TableCell>
              <TableCell>
                <RestoreDialog snapshot={snap} pbsId={pbsId} />
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  );
}
