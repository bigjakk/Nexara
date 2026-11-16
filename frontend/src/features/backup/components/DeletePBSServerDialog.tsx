import { useState } from "react";
import { Trans, useTranslation } from "react-i18next";
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
import { CopyableName } from "@/components/CopyableName";
import { useDeletePBSServer } from "../api/backup-queries";
import { useSaveOutcome } from "@/hooks/useSaveOutcome";
import type { PBSServer } from "../types/backup";

interface DeletePBSServerDialogProps {
  server: PBSServer;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

export function DeletePBSServerDialog({
  server,
  open,
  onOpenChange,
}: DeletePBSServerDialogProps) {
  const { t } = useTranslation("backup");
  const [confirmName, setConfirmName] = useState("");
  const deleteMutation = useDeletePBSServer();
  const settle = useSaveOutcome(open);

  function handleDelete() {
    settle(deleteMutation.mutateAsync(server.id), {
      action: t("deletePBSServerDialog.deletingAction", { name: server.name }),
      onSuccess: () => {
        onOpenChange(false);
      },
      onError: () => undefined, // shown below from the mutation's state
    });
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(v) => {
        onOpenChange(v);
        if (!v) setConfirmName("");
      }}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("deletePBSServerDialog.title")}</DialogTitle>
          <DialogDescription>
            <Trans
              t={t}
              i18nKey="deletePBSServerDialog.description"
              components={{ name: <CopyableName name={server.name} /> }}
            />
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-4 py-2">
          <Input
            placeholder={server.name}
            aria-label={t("serverDialog.confirmLabel")}
            value={confirmName}
            onChange={(e) => {
              setConfirmName(e.target.value);
            }}
          />
          {deleteMutation.isError && (
            <p className="text-sm text-destructive">
              {deleteMutation.error instanceof Error
                ? deleteMutation.error.message
                : t("deleteFailed")}
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
            {t("common:cancel")}
          </Button>
          <Button
            variant="destructive"
            disabled={confirmName !== server.name || deleteMutation.isPending}
            onClick={handleDelete}
          >
            {deleteMutation.isPending
              ? t("common:deleting")
              : t("serverDialog.deleteServer")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
