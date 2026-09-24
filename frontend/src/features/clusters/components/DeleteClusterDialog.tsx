import { useState } from "react";
import { useDeleteCluster } from "@/features/dashboard/api/dashboard-queries";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Checkbox } from "@/components/ui/checkbox";
import { Label } from "@/components/ui/label";
import { CopyableName } from "@/components/CopyableName";
import { usePermissions } from "@/hooks/usePermissions";
import type { ClusterResponse } from "@/types/api";

interface DeleteClusterDialogProps {
  cluster: ClusterResponse;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

export function DeleteClusterDialog({
  cluster,
  open,
  onOpenChange,
}: DeleteClusterDialogProps) {
  const [confirmName, setConfirmName] = useState("");
  const [revokeCredentials, setRevokeCredentials] = useState(false);
  const deleteMutation = useDeleteCluster();
  const { hasPermission } = usePermissions();

  // Two conditions, both required.
  //
  // Nexara must have minted the credential — a token the operator pasted in may
  // be shared with other tooling, and removing it is not something to infer
  // from "stop managing this cluster here".
  //
  // And the caller must hold GLOBAL manage:cluster, matching the server.
  // Deleting a cluster needs only delete:cluster, which can be scoped to one
  // cluster; mutating Proxmox's own access control is a different act and the
  // server rejects it with a 403. Offering a checkbox that turns a working
  // delete into a failed one would be worse than not offering it.
  const canRevoke =
    cluster.credential_source === "bootstrap" &&
    hasPermission("manage", "cluster");

  function handleDelete() {
    deleteMutation.mutate(
      {
        id: cluster.id,
        // What the operator typed, which the button's disabled check has
        // held equal to the cluster's name; the server checks it again.
        confirm: confirmName,
        revokePveCredentials: canRevoke && revokeCredentials,
      },
      {
        onSuccess: () => {
          onOpenChange(false);
        },
      },
    );
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(v) => {
        onOpenChange(v);
        if (!v) {
          setConfirmName("");
          setRevokeCredentials(false);
        }
      }}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Delete Cluster</DialogTitle>
          <DialogDescription>
            This will permanently remove <CopyableName name={cluster.name} />{" "}
            and all associated data (nodes, VMs, metrics). Type the cluster name
            to confirm.
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-4 py-2">
          <Input
            placeholder={cluster.name}
            value={confirmName}
            onChange={(e) => {
              setConfirmName(e.target.value);
            }}
          />
          {canRevoke && (
            <div className="rounded-lg border border-border p-3 space-y-2">
              <div className="flex items-start gap-2">
                <Checkbox
                  id="revoke-pve-credentials"
                  className="mt-0.5"
                  checked={revokeCredentials}
                  onCheckedChange={(checked) => {
                    setRevokeCredentials(Boolean(checked));
                  }}
                />
                <Label
                  htmlFor="revoke-pve-credentials"
                  className="text-sm font-normal leading-snug"
                >
                  Also delete the Proxmox user and API token Nexara created for
                  this cluster
                </Label>
              </div>
              <p className="text-xs text-muted-foreground">
                Leave this unchecked to keep them on the cluster. Nexara removes
                only what it created for this cluster, and skips deleting the
                user entirely if it holds any other API token — so a credential
                you or another install added is never taken with it. If the
                cluster is unreachable the deletion still goes ahead, and
                anything left behind is recorded in the audit log.
              </p>
            </div>
          )}
          {deleteMutation.isError && (
            <p className="text-sm text-destructive">
              {deleteMutation.error instanceof Error
                ? deleteMutation.error.message
                : "Delete failed"}
            </p>
          )}
        </div>
        <DialogFooter>
          <Button
            variant="outline"
            onClick={() => {
              onOpenChange(false);
            }}
          >
            Cancel
          </Button>
          <Button
            variant="destructive"
            disabled={confirmName !== cluster.name || deleteMutation.isPending}
            onClick={handleDelete}
          >
            {deleteMutation.isPending ? "Deleting..." : "Delete Cluster"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
