import { useState } from "react";
import { DatabaseBackup } from "lucide-react";
import { Skeleton } from "@/components/ui/skeleton";
import { useVeeamServers } from "../api/backup-queries";
import type { VeeamServer } from "../types/backup";
import { AddVeeamServerDialog } from "./AddVeeamServerDialog";
import { EditVeeamServerDialog } from "./EditVeeamServerDialog";
import { DeleteVeeamServerDialog } from "./DeleteVeeamServerDialog";
import { VeeamRequirementsNote } from "./VeeamRequirementsNote";
import { VeeamServerTable } from "./VeeamServerTable";

/**
 * The Veeam tab.
 *
 * Phase 1 scope: register a server, see it connect, see its version and
 * licence. Job, session and restore-point views arrive with the inventory
 * sync — until then there is deliberately nothing here pretending to be data.
 */
export function VeeamServersPanel() {
  const serversQuery = useVeeamServers();
  const servers = serversQuery.data ?? [];

  const [editTarget, setEditTarget] = useState<VeeamServer | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<VeeamServer | null>(null);

  if (serversQuery.isLoading) {
    return (
      <div className="space-y-3">
        <Skeleton className="h-10" />
        <Skeleton className="h-24" />
      </div>
    );
  }

  if (serversQuery.isError) {
    return (
      <p className="text-sm text-destructive">
        Failed to load Veeam servers.{" "}
        {serversQuery.error instanceof Error ? serversQuery.error.message : ""}
      </p>
    );
  }

  if (servers.length === 0) {
    return (
      <div className="space-y-4">
        <div className="rounded-md border bg-muted/50 px-6 py-12 text-center">
          <DatabaseBackup className="mx-auto mb-3 h-10 w-10 text-muted-foreground" />
          <h2 className="text-lg font-medium">No Veeam Servers</h2>
          <p className="mx-auto mt-1 mb-4 max-w-lg text-sm text-muted-foreground">
            Connect a Veeam Backup &amp; Replication server to see the state of
            its Proxmox backup jobs alongside your clusters.
          </p>
          <AddVeeamServerDialog />
        </div>
        <VeeamRequirementsNote />
      </div>
    );
  }

  return (
    <div className="space-y-4">
      <div className="flex justify-end">
        <AddVeeamServerDialog />
      </div>

      <VeeamServerTable
        servers={servers}
        onEdit={setEditTarget}
        onDelete={setDeleteTarget}
      />

      {editTarget != null && (
        <EditVeeamServerDialog
          server={editTarget}
          open
          onOpenChange={(open) => {
            if (!open) setEditTarget(null);
          }}
        />
      )}
      {deleteTarget != null && (
        <DeleteVeeamServerDialog
          server={deleteTarget}
          open
          onOpenChange={(open) => {
            if (!open) setDeleteTarget(null);
          }}
        />
      )}
    </div>
  );
}
