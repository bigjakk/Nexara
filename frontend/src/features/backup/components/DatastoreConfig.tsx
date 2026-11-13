import { PropertyList, PropertyRow } from "@/components/page/Panel";
import { Skeleton } from "@/components/ui/skeleton";
import { useDatastoreConfig, usePruneJobs } from "../api/backup-queries";
import { RETENTION_KEYS, summarizePrune } from "../lib/prune-summary";

interface DatastoreConfigProps {
  pbsId: string;
  store: string;
}

const WARN_TEXT = "text-amber-700 dark:text-amber-400";

/**
 * How a datastore is set up on the server: where it lives and what runs on
 * it. A list of facts for the side of its panel, not a block of its own.
 */
export function DatastoreConfig({ pbsId, store }: DatastoreConfigProps) {
  const { data: config, isLoading } = useDatastoreConfig(pbsId, store);
  // Prune lives outside datastore.cfg on PBS >= 2.2, so the datastore's own
  // config cannot answer "is this pruned?" on its own.
  const {
    data: pruneJobs,
    isLoading: pruneLoading,
    isError: pruneFailed,
  } = usePruneJobs(pbsId, store);

  if (isLoading) {
    return <Skeleton className="h-32" />;
  }

  if (!config) {
    return (
      <p className="text-sm text-muted-foreground">
        This datastore&apos;s configuration could not be read.
      </p>
    );
  }

  const prune = summarizePrune(config["prune-schedule"], config, pruneJobs, {
    loading: pruneLoading,
    failed: pruneFailed,
  });

  const keeps = prune.retention
    ? RETENTION_KEYS.map(([key, label]) => ({
        label,
        value: prune.retention?.[key],
      })).filter((d) => d.value != null && d.value > 0)
    : [];

  return (
    <PropertyList>
      {config.path && (
        <PropertyRow label="Path">
          <span className="font-mono text-[12.5px]">{config.path}</span>
        </PropertyRow>
      )}
      <PropertyRow label="Garbage collection">
        {config["gc-schedule"] || (
          <span className="text-muted-foreground">Not set</span>
        )}
      </PropertyRow>
      <PropertyRow label="Prune">{prune.scheduleLabel}</PropertyRow>
      {keeps.length > 0 && (
        <PropertyRow label="Keeps">
          {keeps.map((d) => `${d.label} ${String(d.value)}`).join(", ")}
        </PropertyRow>
      )}
      {prune.retentionNote && (
        <PropertyRow label="Keeps">{prune.retentionNote}</PropertyRow>
      )}
      {prune.lastRun && (
        <PropertyRow label="Last prune">
          {new Date(prune.lastRun.at * 1000).toLocaleString()}
        </PropertyRow>
      )}
      {prune.nextRun != null && (
        <PropertyRow label="Next prune">
          {new Date(prune.nextRun * 1000).toLocaleString()}
        </PropertyRow>
      )}
      {prune.failedJob && (
        <PropertyRow label="Prune job">
          <span className={WARN_TEXT}>
            {prune.failedJob.id} last ended {prune.failedJob.state}
          </span>
        </PropertyRow>
      )}
      <PropertyRow label="Verify new backups">
        {config["verify-new"] ? "Yes" : "No"}
      </PropertyRow>
      {config["maintenance-mode"] && (
        <PropertyRow label="Maintenance">
          <span className={WARN_TEXT}>{config["maintenance-mode"]}</span>
        </PropertyRow>
      )}
      {config.comment && (
        <PropertyRow label="Comment">{config.comment}</PropertyRow>
      )}
    </PropertyList>
  );
}
