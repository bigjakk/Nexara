import { useEffect, useState } from "react";
import { Upload } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Segmented } from "@/components/ui/segmented";
import { PageHeader } from "@/components/page/PageHeader";
import { usePermissions } from "@/hooks/usePermissions";
import { useCreateResourceStore } from "@/stores/create-resource-store";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import { useImportJobs, useImportSources } from "../api/import-queries";
import { ImportHistoryTable } from "../components/ImportHistoryTable";
import { ImportSourcesManager } from "../components/ImportSourcesManager";
import { jobSummary, plural } from "@/features/migrations/lib/job-summary";

/** More clusters than this and the picker is a menu, not a row of segments. */
const SEGMENTED_CLUSTER_LIMIT = 4;

export function ImportsPage() {
  const { data: clusters } = useClusters();
  const [clusterId, setClusterId] = useState("");
  const { canManage } = usePermissions();
  const requestCreate = useCreateResourceStore((s) => s.request);

  useEffect(() => {
    if (!clusterId && clusters && clusters.length > 0 && clusters[0]) {
      setClusterId(clusters[0].id);
    }
  }, [clusters, clusterId]);

  // The same reads the two panels below make, so the sentence costs no
  // request of its own.
  const { data: jobs } = useImportJobs(clusterId);
  const { data: sources } = useImportSources(clusterId);
  const summary =
    jobs !== undefined && sources !== undefined
      ? `${
          jobSummary(
            "import",
            jobs.map((j) => j.status),
            ["pending", "running"],
          ) ?? "No imports yet"
        }, ${plural(new Set(sources.map((s) => s.storage)).size, "source")} to import from`
      : "Guests imported from OVA and OVF appliances, disk images and ESXi or vCenter hosts.";

  // The dialog the Create menu opens, started here for the cluster on show.
  const importButton =
    clusterId !== "" && canManage("vm_import") ? (
      <Button
        size="sm"
        className="gap-1.5"
        onClick={() => {
          requestCreate("import", clusterId);
        }}
      >
        <Upload />
        Import VM
      </Button>
    ) : undefined;

  return (
    <div className="space-y-5">
      <PageHeader
        title="Imports"
        summary={summary}
        actions={
          <>
            {clusters &&
              clusters.length > 1 &&
              (clusters.length <= SEGMENTED_CLUSTER_LIMIT ? (
                <Segmented
                  aria-label="Cluster"
                  size="md"
                  value={clusterId}
                  onValueChange={setClusterId}
                  options={clusters.map((c) => ({
                    value: c.id,
                    label: c.name,
                  }))}
                />
              ) : (
                <Select value={clusterId} onValueChange={setClusterId}>
                  <SelectTrigger className="h-8 w-52" aria-label="Cluster">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {clusters.map((c) => (
                      <SelectItem key={c.id} value={c.id}>
                        {c.name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              ))}
            {importButton}
          </>
        }
      />

      {/* What was imported on the left, where imports come from on the
          right; the rail folds under on a narrow screen. */}
      {clusterId && (
        <div className="flex flex-wrap items-start gap-4">
          <div className="min-w-0 flex-[999_1_560px]">
            <ImportHistoryTable clusterId={clusterId} />
          </div>
          <div className="min-w-0 flex-[1_1_340px]">
            <ImportSourcesManager clusterId={clusterId} />
          </div>
        </div>
      )}
    </div>
  );
}
