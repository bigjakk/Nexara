import { useEffect } from "react";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import { useCreateResourceStore } from "@/stores/create-resource-store";
import { CreateVMDialog } from "@/features/vms/components/CreateVMDialog";
import { CreateCTDialog } from "@/features/vms/components/CreateCTDialog";
import { ImportVMDialog } from "@/features/vms/components/ImportVMDialog";

// Mounted once (in AppShell). Owns the create/import dialogs and the
// multi-cluster picker so every entry point only has to call the store.
export function CreateResourceDialogs() {
  const { data: clusters } = useClusters();
  const dialog = useCreateResourceStore((s) => s.dialog);
  const clusterId = useCreateResourceStore((s) => s.clusterId);
  const pendingType = useCreateResourceStore((s) => s.pendingType);
  const pickCluster = useCreateResourceStore((s) => s.pickCluster);
  const cancelPending = useCreateResourceStore((s) => s.cancelPending);
  const close = useCreateResourceStore((s) => s.close);

  // Exactly one cluster → skip the picker entirely.
  useEffect(() => {
    if (pendingType && clusters && clusters.length === 1 && clusters[0]) {
      pickCluster(clusters[0].id);
    }
  }, [pendingType, clusters, pickCluster]);

  const showPicker = pendingType !== null && (clusters?.length ?? 0) > 1;
  const verb =
    pendingType === "import"
      ? "import into"
      : pendingType === "ct"
        ? "create the container on"
        : "create the VM on";

  return (
    <>
      <Dialog
        open={showPicker}
        onOpenChange={(o) => {
          if (!o) cancelPending();
        }}
      >
        <DialogContent className="max-w-sm">
          <DialogHeader>
            <DialogTitle>Select Cluster</DialogTitle>
          </DialogHeader>
          <div className="space-y-2">
            <Label>Choose which cluster to {verb}</Label>
            <div className="space-y-1">
              {clusters?.map((cluster) => (
                <Button
                  key={cluster.id}
                  variant="outline"
                  className="w-full justify-start"
                  onClick={() => {
                    pickCluster(cluster.id);
                  }}
                >
                  {cluster.name}
                </Button>
              ))}
            </div>
          </div>
        </DialogContent>
      </Dialog>

      <CreateVMDialog
        open={dialog === "vm"}
        onOpenChange={(o) => {
          if (!o) close();
        }}
        clusterId={clusterId}
      />
      <CreateCTDialog
        open={dialog === "ct"}
        onOpenChange={(o) => {
          if (!o) close();
        }}
        clusterId={clusterId}
      />
      <ImportVMDialog
        open={dialog === "import"}
        onOpenChange={(o) => {
          if (!o) close();
        }}
        clusterId={clusterId}
      />
    </>
  );
}
