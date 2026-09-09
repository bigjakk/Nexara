import { FileText, Loader2 } from "lucide-react";

import { Button } from "@/components/ui/button";
import { useDownloadNodeReport } from "../../api/cluster-queries";

/**
 * Downloads a node's `pvereport` support bundle — the artefact to attach when
 * asking for help with a cluster.
 *
 * It is generated on demand, and Proxmox shells out to a long list of commands
 * to build it, so the button holds a pending state for however long that takes
 * rather than pretending the download was instant.
 */
export function NodeReportButton({
  clusterId,
  nodeName,
}: {
  clusterId: string;
  nodeName: string;
}) {
  const download = useDownloadNodeReport(clusterId, nodeName);

  return (
    <div className="flex flex-col items-end gap-1">
      <Button
        variant="outline"
        size="sm"
        className="gap-1.5"
        disabled={download.isPending}
        onClick={() => {
          download.mutate();
        }}
        title="Download this node's pvereport support bundle"
      >
        {download.isPending ? (
          <Loader2 className="h-4 w-4 animate-spin" />
        ) : (
          <FileText className="h-4 w-4" />
        )}
        {download.isPending ? "Generating…" : "Support Bundle"}
      </Button>
      {download.error && (
        <span className="text-xs text-destructive">
          {download.error instanceof Error
            ? download.error.message
            : "Failed to download the report."}
        </span>
      )}
    </div>
  );
}
