import { Trans, useTranslation } from "react-i18next";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { useDeleteSnapshot } from "../api/backup-queries";
import type { PBSSnapshot } from "../types/backup";

interface DeleteSnapshotDialogProps {
  snapshot: PBSSnapshot;
  pbsId: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

export function DeleteSnapshotDialog({
  snapshot,
  pbsId,
  open,
  onOpenChange,
}: DeleteSnapshotDialogProps) {
  const { t } = useTranslation("backup");
  const deleteMutation = useDeleteSnapshot();

  const handleDelete = () => {
    deleteMutation.mutate(
      {
        pbsId,
        store: snapshot.datastore,
        body: {
          backup_type: snapshot.backup_type,
          backup_id: snapshot.backup_id,
          backup_time: snapshot.backup_time,
        },
      },
      {
        onSuccess: () => {
          onOpenChange(false);
        },
      },
    );
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("deleteSnapshotDialog.title")}</DialogTitle>
          <DialogDescription>
            <Trans
              t={t}
              i18nKey="deleteSnapshotDialog.description"
              values={{
                type: snapshot.backup_type,
                id: snapshot.backup_id,
                datastore: snapshot.datastore,
              }}
              components={{ strong: <strong /> }}
            />
          </DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <Button
            variant="outline"
            onClick={() => {
              onOpenChange(false);
            }}
          >
            {t("common:cancel")}
          </Button>
          <Button
            variant="destructive"
            onClick={handleDelete}
            disabled={deleteMutation.isPending}
          >
            {deleteMutation.isPending
              ? t("common:deleting")
              : t("common:delete")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
