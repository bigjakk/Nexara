import { HardDrive } from "lucide-react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import type { PBSDatastoreStatus } from "../types/backup";

function formatBytes(bytes: number): string {
  if (bytes === 0) return "0 B";
  const k = 1024;
  const sizes = ["B", "KB", "MB", "GB", "TB", "PB"];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  return `${(bytes / Math.pow(k, i)).toFixed(1)} ${sizes[i] ?? ""}`;
}

interface DatastoreCardsProps {
  datastores: PBSDatastoreStatus[];
}

export function DatastoreCards({ datastores }: DatastoreCardsProps) {
  if (datastores.length === 0) {
    return (
      <p className="text-sm text-muted-foreground">No datastores found.</p>
    );
  }

  return (
    <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-3">
      {datastores.map((ds) => {
        const usedPct = ds.total > 0 ? (ds.used / ds.total) * 100 : 0;
        const barColor =
          usedPct > 90
            ? "bg-destructive"
            : usedPct > 75
              ? "bg-yellow-500"
              : "bg-primary";

        return (
          <Card key={ds.store}>
            <CardHeader className="flex flex-row items-center justify-between pb-2">
              <CardTitle className="text-sm font-medium">{ds.store}</CardTitle>
              <HardDrive className="h-4 w-4 text-muted-foreground" />
            </CardHeader>
            <CardContent className="space-y-3">
              <div className="flex justify-between text-sm">
                <span className="text-muted-foreground">Used</span>
                <span className="font-medium">
                  {formatBytes(ds.used)} / {formatBytes(ds.total)}
                </span>
              </div>
              <div className="h-2 w-full overflow-hidden rounded-full bg-muted">
                <div
                  className={`h-full rounded-full transition-all ${barColor}`}
                  style={{ width: `${Math.min(usedPct, 100)}%` }}
                />
              </div>
              <div className="flex justify-between text-xs text-muted-foreground">
                <span>{usedPct.toFixed(1)}% used</span>
                <span>{formatBytes(ds.avail)} free</span>
              </div>
            </CardContent>
          </Card>
        );
      })}
    </div>
  );
}
