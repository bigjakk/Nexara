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
import { useUpdateVMFolder } from "../api/folder-queries";
import { useSaveOutcome } from "@/hooks/useSaveOutcome";

interface RenameFolderDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  clusterId: string;
  folderId: string;
  currentName: string;
}

export function RenameFolderDialog({
  open,
  onOpenChange,
  clusterId,
  folderId,
  currentName,
}: RenameFolderDialogProps) {
  const update = useUpdateVMFolder();
  // The dialog shows a failure itself (the hook opts out of the app's toast),
  // and one that lands after it was closed is toasted instead.
  const settle = useSaveOutcome(open ? folderId : null);
  const [name, setName] = useState(currentName);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (open) {
      setName(currentName);
      setError(null);
    }
  }, [open, currentName]);

  function handleSubmit() {
    const trimmed = name.trim();
    if (!trimmed) {
      setError("Folder name is required");
      return;
    }
    if (trimmed === currentName) {
      onOpenChange(false);
      return;
    }
    setError(null);
    settle(update.mutateAsync({ clusterId, folderId, name: trimmed }), {
      action: `Renaming folder ${currentName}`,
      onSuccess: () => {
        onOpenChange(false);
      },
      onError: (err: unknown) => {
        setError(
          err instanceof Error ? err.message : "Failed to rename folder",
        );
      },
    });
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Rename folder</DialogTitle>
          <DialogDescription>
            A new name for <strong>{currentName}</strong>. The guests in it stay
            where they are.
          </DialogDescription>
        </DialogHeader>

        <Field label="Name" htmlFor="rename-folder-name" error={error}>
          <Input
            id="rename-folder-name"
            value={name}
            onChange={(e) => {
              setName(e.target.value);
            }}
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
            disabled={update.isPending}
          >
            Cancel
          </Button>
          <Button onClick={handleSubmit} disabled={update.isPending}>
            {update.isPending ? "Renaming…" : "Rename"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
