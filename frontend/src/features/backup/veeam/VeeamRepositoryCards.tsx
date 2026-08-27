import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { HardDrive } from "lucide-react";
import { formatBytes } from "@/lib/format";
import type { VeeamRepository } from "../types/backup";

interface VeeamRepositoryCardsProps {
  repositories: VeeamRepository[];
}

/**
 * Repositories are a GLOBAL resource, not a per-cluster one: a single
 * repository holds the backups of every cluster the Veeam server protects.
 * That is why they are not filtered by cluster anywhere in this feature.
 */
export function VeeamRepositoryCards({
  repositories,
}: VeeamRepositoryCardsProps) {
  if (repositories.length === 0) {
    return (
      <p className="py-8 text-center text-sm text-muted-foreground">
        No repositories reported by this server.
      </p>
    );
  }

  return (
    <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-3">
      {repositories.map((repo) => {
        // Veeam reports capacity as floating-point GB and the client converts
        // to bytes; a target it cannot measure comes back as zero, which must
        // read as "unknown" rather than as a full disk.
        const known = repo.capacity_bytes > 0;
        const usedPct = known
          ? Math.min((repo.used_bytes / repo.capacity_bytes) * 100, 100)
          : 0;

        const barColor =
          usedPct >= 90
            ? "bg-destructive"
            : usedPct >= 75
              ? "bg-amber-500"
              : "bg-primary";

        return (
          <Card key={repo.id}>
            <CardHeader className="pb-2">
              <div className="flex items-start justify-between gap-2">
                <CardTitle className="flex items-center gap-2 text-base">
                  <HardDrive className="h-4 w-4 shrink-0 text-muted-foreground" />
                  <span className="break-all">{repo.name}</span>
                </CardTitle>
                {!repo.is_online && (
                  <Badge variant="destructive">Offline</Badge>
                )}
              </div>
              <p className="text-xs text-muted-foreground">{repo.type}</p>
            </CardHeader>
            <CardContent className="space-y-2">
              {known ? (
                <>
                  <div className="flex justify-between text-sm">
                    <span className="text-muted-foreground">Used</span>
                    <span className="font-mono">
                      {formatBytes(repo.used_bytes)} /{" "}
                      {formatBytes(repo.capacity_bytes)}
                    </span>
                  </div>
                  <div className="h-2 w-full overflow-hidden rounded-full bg-muted">
                    <div
                      className={`h-full rounded-full transition-all ${barColor}`}
                      style={{ width: `${String(usedPct)}%` }}
                    />
                  </div>
                  <div className="flex justify-between text-xs text-muted-foreground">
                    <span>{usedPct.toFixed(1)}% used</span>
                    <span>{formatBytes(repo.free_bytes)} free</span>
                  </div>
                </>
              ) : (
                <p className="text-sm text-muted-foreground">
                  Veeam reports no capacity for this target.
                </p>
              )}

              {repo.path !== "" && (
                <p className="break-all pt-1 font-mono text-xs text-muted-foreground">
                  {repo.path}
                </p>
              )}
            </CardContent>
          </Card>
        );
      })}
    </div>
  );
}
