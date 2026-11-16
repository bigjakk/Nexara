import { useEffect, useState } from "react";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Field } from "@/components/page/Field";
import { useCreateVMFolder } from "../api/folder-queries";
import { useSaveOutcome } from "@/hooks/useSaveOutcome";
import { useTranslation } from "react-i18next";

interface CreateFolderDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  clusterId: string;
  /** If null, the new folder is created at the top level of the cluster. */
  parentId: string | null;
  /** Display name for the parent context (e.g. cluster name or folder name). */
  parentLabel: string;
}

export function CreateFolderDialog({
  open,
  onOpenChange,
  clusterId,
  parentId,
  parentLabel,
}: CreateFolderDialogProps) {
  const { t } = useTranslation("vms");
  const create = useCreateVMFolder();
  // The dialog shows a failure itself (the hook opts out of the app's toast),
  // and one that lands after it was closed is toasted instead.
  const settle = useSaveOutcome(open);
  const [name, setName] = useState("");
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (open) {
      setName("");
      setError(null);
    }
  }, [open]);

  function handleSubmit() {
    const trimmed = name.trim();
    if (!trimmed) {
      setError("Folder name is required");
      return;
    }
    setError(null);
    settle(
      create.mutateAsync({ clusterId, name: trimmed, parent_id: parentId }),
      {
        action: t("createFolderDialogActions.action", {
          trimmed,
        }),
        onSuccess: () => {
          onOpenChange(false);
        },
        onError: (err: unknown) => {
          setError(
            err instanceof Error ? err.message : "Failed to create folder",
          );
        },
      },
    );
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>New folder</DialogTitle>
          <DialogDescription>
            A folder inside <strong>{parentLabel}</strong>, for grouping guests
            in the tree.
          </DialogDescription>
        </DialogHeader>

        <Field label="Name" htmlFor="folder-name" error={error}>
          <Input
            id="folder-name"
            value={name}
            onChange={(e) => {
              setName(e.target.value);
            }}
            placeholder="e.g. Production"
            maxLength={128}
            autoFocus
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                e.preventDefault();
                handleSubmit();
              }
            }}
          />
        </Field>

        <DialogFooter className="gap-2 sm:gap-0">
          <Button
            variant="outline"
            onClick={() => {
              onOpenChange(false);
            }}
            disabled={create.isPending}
          >
            Cancel
          </Button>
          <Button onClick={handleSubmit} disabled={create.isPending}>
            {create.isPending ? "Creating…" : "Create folder"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
