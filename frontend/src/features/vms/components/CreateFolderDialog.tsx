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
import { useCreateVMFolder } from "../api/folder-queries";

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
  const create = useCreateVMFolder();
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
    create.mutate(
      { clusterId, name: trimmed, parent_id: parentId },
      {
        onSuccess: () => {
          onOpenChange(false);
        },
        onError: (err: unknown) => {
          setError(err instanceof Error ? err.message : "Failed to create folder");
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
            Creating a folder inside <strong>{parentLabel}</strong>.
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-2">
          <label className="text-sm font-medium" htmlFor="folder-name">
            Name
          </label>
          <Input
            id="folder-name"
            value={name}
            onChange={(e) => { setName(e.target.value); }}
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
        </div>

        {error && <p className="text-sm text-destructive">{error}</p>}

        <DialogFooter>
          <Button
            variant="outline"
            onClick={() => { onOpenChange(false); }}
            disabled={create.isPending}
          >
            Cancel
          </Button>
          <Button onClick={handleSubmit} disabled={create.isPending}>
            {create.isPending ? "Creating…" : "Create"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
