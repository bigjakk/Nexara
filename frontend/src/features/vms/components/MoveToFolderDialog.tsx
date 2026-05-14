import { useMemo, useState, useEffect } from "react";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { useVMFolders, useAssignVMToFolder } from "../api/folder-queries";
import { buildFolderTree, flattenFolderTree } from "../lib/folder-tree";

interface MoveToFolderDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  clusterId: string;
  vmId: string;
  vmName: string;
}

export function MoveToFolderDialog({
  open,
  onOpenChange,
  clusterId,
  vmId,
  vmName,
}: MoveToFolderDialogProps) {
  const foldersQuery = useVMFolders(open ? clusterId : "");
  const assign = useAssignVMToFolder();
  const [selected, setSelected] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  // Pre-select the current folder when the dialog opens.
  useEffect(() => {
    if (!open) return;
    const current = foldersQuery.data?.memberships.find((m) => m.vm_id === vmId);
    setSelected(current ? current.folder_id : null);
    setError(null);
  }, [open, foldersQuery.data, vmId]);

  const flat = useMemo(() => {
    if (!foldersQuery.data) return [];
    const tree = buildFolderTree(foldersQuery.data.folders);
    return flattenFolderTree(tree);
  }, [foldersQuery.data]);

  function handleSubmit() {
    setError(null);
    assign.mutate(
      { clusterId, vmId, folder_id: selected },
      {
        onSuccess: () => {
          onOpenChange(false);
        },
        onError: (err: unknown) => {
          setError(err instanceof Error ? err.message : "Failed to move VM");
        },
      },
    );
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Move to folder</DialogTitle>
          <DialogDescription>
            Choose a folder for <strong>{vmName}</strong>. Folders are only used
            for organising the tree view; the VM is not migrated between hosts.
          </DialogDescription>
        </DialogHeader>

        {foldersQuery.isLoading ? (
          <div className="space-y-2">
            <Skeleton className="h-8 w-full" />
            <Skeleton className="h-8 w-full" />
          </div>
        ) : (
          <div className="max-h-[300px] space-y-0.5 overflow-y-auto rounded-md border p-1">
            <button
              type="button"
              onClick={() => { setSelected(null); }}
              className={`flex w-full items-center rounded px-2 py-1 text-left text-sm transition-colors ${
                selected === null
                  ? "bg-accent text-accent-foreground"
                  : "hover:bg-accent/50"
              }`}
            >
              <span className="text-muted-foreground">(Unassigned — Discovered)</span>
            </button>
            {flat.length === 0 && (
              <p className="px-2 py-2 text-xs text-muted-foreground">
                No folders yet — create one from the tree right-click menu.
              </p>
            )}
            {flat.map(({ folder, depth }) => (
              <button
                key={folder.id}
                type="button"
                onClick={() => { setSelected(folder.id); }}
                className={`flex w-full items-center rounded px-2 py-1 text-left text-sm transition-colors ${
                  selected === folder.id
                    ? "bg-accent text-accent-foreground"
                    : "hover:bg-accent/50"
                }`}
                style={{ paddingLeft: `${String(depth * 16 + 8)}px` }}
              >
                {folder.name}
              </button>
            ))}
          </div>
        )}

        {error && <p className="text-sm text-destructive">{error}</p>}

        <DialogFooter>
          <Button
            variant="outline"
            onClick={() => { onOpenChange(false); }}
            disabled={assign.isPending}
          >
            Cancel
          </Button>
          <Button onClick={handleSubmit} disabled={assign.isPending}>
            {assign.isPending ? "Moving…" : "Move"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
