import { useMemo, useState, useEffect, type ReactNode } from "react";
import { Check, Folder, FolderX } from "lucide-react";
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
import { Field } from "@/components/page/Field";
import { cn } from "@/lib/utils";
import { useVMFolders, useAssignVMToFolder } from "../api/folder-queries";
import { useSaveOutcome } from "@/hooks/useSaveOutcome";
import { buildFolderTree, flattenFolderTree } from "../lib/folder-tree";
import { useTranslation } from "react-i18next";

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
  const { t } = useTranslation("vms");
  const foldersQuery = useVMFolders(open ? clusterId : "");
  const assign = useAssignVMToFolder(true);
  // The dialog shows a failure itself (the hook opts out of the app's toast),
  // and one that lands after it was closed is toasted instead.
  const settle = useSaveOutcome(open ? vmId : null);
  const [selected, setSelected] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  // Pre-select the current folder when the dialog opens.
  useEffect(() => {
    if (!open) return;
    const current = foldersQuery.data?.memberships.find(
      (m) => m.vm_id === vmId,
    );
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
    settle(assign.mutateAsync({ clusterId, vmId, folder_id: selected }), {
      action: t("moveToFolderDialogActions.action", { vmName }),
      onSuccess: () => {
        onOpenChange(false);
      },
      onError: (err: unknown) => {
        setError(err instanceof Error ? err.message : "Failed to move VM");
      },
    });
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Move to folder</DialogTitle>
          <DialogDescription>
            Where <strong>{vmName}</strong> sits in the tree. Folders only
            organise the view: the guest is not migrated between hosts.
          </DialogDescription>
        </DialogHeader>

        <Field
          label="Folder"
          hint={
            !foldersQuery.isLoading &&
            flat.length === 0 &&
            "No folders yet. Create one from the tree's right-click menu."
          }
          error={error}
        >
          {foldersQuery.isLoading ? (
            <div className="space-y-1.5">
              <Skeleton className="h-8 w-full" />
              <Skeleton className="h-8 w-full" />
            </div>
          ) : (
            <div
              role="group"
              aria-label="Folder"
              className="max-h-[300px] space-y-px overflow-y-auto rounded-[10px] bg-foreground/[0.04] p-1"
            >
              <FolderOption
                selected={selected === null}
                onSelect={() => {
                  setSelected(null);
                }}
                icon={<FolderX />}
                muted
              >
                No folder (listed under Discovered)
              </FolderOption>
              {flat.map(({ folder, depth }) => (
                <FolderOption
                  key={folder.id}
                  selected={selected === folder.id}
                  onSelect={() => {
                    setSelected(folder.id);
                  }}
                  icon={<Folder />}
                  depth={depth}
                >
                  {folder.name}
                </FolderOption>
              ))}
            </div>
          )}
        </Field>

        <DialogFooter className="gap-2 sm:gap-0">
          <Button
            variant="outline"
            onClick={() => {
              onOpenChange(false);
            }}
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

/** One row of the folder list: the chosen one carries the tick. */
function FolderOption({
  selected,
  onSelect,
  icon,
  depth = 0,
  muted = false,
  children,
}: {
  selected: boolean;
  onSelect: () => void;
  icon: ReactNode;
  /** How deep the folder sits; each level indents the row. */
  depth?: number;
  /** Not a folder: the "no folder" choice reads quieter than a name. */
  muted?: boolean;
  children: ReactNode;
}) {
  return (
    <button
      type="button"
      aria-pressed={selected}
      onClick={onSelect}
      className={cn(
        "flex w-full items-center gap-2 rounded-md py-1.5 pr-2 text-left text-sm outline-hidden transition-colors focus-visible:ring-2 focus-visible:ring-ring [&_svg]:size-4 [&_svg]:shrink-0",
        selected
          ? "bg-primary/10 font-medium text-foreground"
          : "hover:bg-foreground/[0.06]",
        muted && !selected && "text-muted-foreground",
      )}
      style={{ paddingLeft: `${String(depth * 16 + 8)}px` }}
    >
      <span
        aria-hidden="true"
        className={selected ? "text-primary" : "text-muted-foreground"}
      >
        {icon}
      </span>
      <span className="min-w-0 flex-1 truncate">{children}</span>
      {selected && <Check aria-hidden="true" className="text-primary" />}
    </button>
  );
}
