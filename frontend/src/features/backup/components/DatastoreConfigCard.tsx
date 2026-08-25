import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { useDatastoreConfig, usePruneJobs } from "../api/backup-queries";
import { RETENTION_KEYS, summarizePrune } from "../lib/prune-summary";

interface DatastoreConfigCardProps {
  pbsId: string;
  store: string;
}

export function DatastoreConfigCard({ pbsId, store }: DatastoreConfigCardProps) {
  const { data: config, isLoading } = useDatastoreConfig(pbsId, store);
  // Prune lives outside datastore.cfg on PBS >= 2.2, so the datastore's own
  // config cannot answer "is this pruned?" on its own.
  const {
    data: pruneJobs,
    isLoading: pruneLoading,
    isError: pruneFailed,
  } = usePruneJobs(pbsId, store);

  if (isLoading) {
    return <Skeleton className="h-40" />;
  }

  if (!config) {
    return null;
  }

  const prune = summarizePrune(config["prune-schedule"], config, pruneJobs, {
    loading: pruneLoading,
    failed: pruneFailed,
  });

  const pruneDefaults = prune.retention
    ? RETENTION_KEYS.map(([key, label]) => ({
        label,
        value: prune.retention?.[key],
      })).filter((d) => d.value != null && d.value > 0)
    : [];

  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="text-sm font-medium">
          {store} Configuration
        </CardTitle>
      </CardHeader>
      <CardContent>
        <div className="grid gap-3 text-sm sm:grid-cols-2 lg:grid-cols-3">
          {config.path && (
            <div>
              <span className="text-muted-foreground">Path:</span>{" "}
              <span className="font-mono text-xs">{config.path}</span>
            </div>
          )}
          <div>
            <span className="text-muted-foreground">GC Schedule:</span>{" "}
            {config["gc-schedule"] || "Not set"}
          </div>
          <div>
            <span className="text-muted-foreground">Prune Schedule:</span>{" "}
            {prune.scheduleLabel}
          </div>
          <div>
            <span className="text-muted-foreground">Verify New:</span>{" "}
            {config["verify-new"] ? "Yes" : "No"}
          </div>
          {config["maintenance-mode"] && (
            <div>
              <span className="text-muted-foreground">Maintenance:</span>{" "}
              <span className="text-amber-600">{config["maintenance-mode"]}</span>
            </div>
          )}
          {prune.lastRun && (
            <div>
              <span className="text-muted-foreground">Last Prune:</span>{" "}
              {new Date(prune.lastRun.at * 1000).toLocaleString()}
            </div>
          )}
          {prune.nextRun != null && (
            <div>
              <span className="text-muted-foreground">Next Prune:</span>{" "}
              {new Date(prune.nextRun * 1000).toLocaleString()}
            </div>
          )}
          {prune.failedJob && (
            <div>
              <span className="text-muted-foreground">Prune Job:</span>{" "}
              <span className="text-amber-600">
                {prune.failedJob.id} last ended {prune.failedJob.state}
              </span>
            </div>
          )}
          {pruneDefaults.length > 0 && (
            <div className="sm:col-span-2 lg:col-span-3">
              <span className="text-muted-foreground">Prune Defaults:</span>{" "}
              {pruneDefaults.map((d) => `${d.label}: ${String(d.value)}`).join(", ")}
            </div>
          )}
          {prune.retentionNote && (
            <div className="sm:col-span-2 lg:col-span-3">
              <span className="text-muted-foreground">Prune Defaults:</span>{" "}
              {prune.retentionNote}
            </div>
          )}
          {config.comment && (
            <div className="sm:col-span-2 lg:col-span-3">
              <span className="text-muted-foreground">Comment:</span>{" "}
              {config.comment}
            </div>
          )}
        </div>
      </CardContent>
    </Card>
  );
}
