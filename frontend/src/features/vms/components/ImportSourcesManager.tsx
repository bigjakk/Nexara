import { useMemo, useRef, useState } from "react";
import { Trash2, Plus } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Panel } from "@/components/page/Panel";
import { cn } from "@/lib/utils";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { usePermissions } from "@/hooks/usePermissions";
import { useDeleteImportSource, useImportSources } from "../api/import-queries";
import { useSaveOutcome } from "@/hooks/useSaveOutcome";
import { EsxiSourceForm } from "./EsxiSourceForm";
import type { ImportSource } from "@/types/api";
import { useTranslation } from "react-i18next";

interface ImportSourcesManagerProps {
  clusterId: string;
}

interface SourceGroup {
  storage: string;
  type: string;
  shared: boolean;
  nodes: string[];
  removesStore: boolean;
}

/**
 * Whether removing a source deletes its storage definition: an ESXi source, or
 * a store holding nothing but imports. A store that holds other content too
 * loses only the import content type, which takes manage:storage.
 */
function removesStore(source: ImportSource): boolean {
  return (
    source.type === "esxi" ||
    source.content
      .split(",")
      .map((c) => c.trim())
      .every((c) => c === "" || c === "import")
  );
}

export function ImportSourcesManager({ clusterId }: ImportSourcesManagerProps) {
  const { t } = useTranslation("vms");
  const { data: sources } = useImportSources(clusterId);
  const { canManage } = usePermissions();
  const deleteMutation = useDeleteImportSource();
  const [showEsxiForm, setShowEsxiForm] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState<string | null>(null);
  const settle = useSaveOutcome(deleteTarget);
  // Where focus goes when Remove closes: the list, whose row goes with the
  // source.
  const section = useRef<HTMLDivElement>(null);

  const groups = useMemo<SourceGroup[]>(() => {
    const byStorage = new Map<string, SourceGroup>();
    (sources ?? []).forEach((s: ImportSource) => {
      const g = byStorage.get(s.storage);
      if (g) {
        if (!g.nodes.includes(s.node)) g.nodes.push(s.node);
      } else {
        byStorage.set(s.storage, {
          storage: s.storage,
          type: s.type,
          shared: s.shared,
          nodes: [s.node],
          removesStore: removesStore(s),
        });
      }
    });
    return [...byStorage.values()].sort((a, b) =>
      a.storage.localeCompare(b.storage),
    );
  }, [sources]);

  const canManageImport = canManage("vm_import");
  // Enabling the import content type edits a store, and so does removing it
  // from a store that holds other content.
  const canManageStorage = canManage("storage");
  const target = groups.find((g) => g.storage === deleteTarget);

  function confirmDelete() {
    if (!deleteTarget) return;
    // A failure keeps the dialog open, which shows it.
    settle(deleteMutation.mutateAsync({ clusterId, storage: deleteTarget }), {
      action: t("importSourcesManagerActions.action", { deleteTarget }),
      onSuccess: () => {
        setDeleteTarget(null);
      },
      onError: () => undefined, // shown in the dialog from the mutation's state
    });
  }

  return (
    <Panel
      title="Import sources"
      aside={groups.length > 0 ? String(groups.length) : undefined}
      actions={
        canManageImport && !showEsxiForm ? (
          <Button
            type="button"
            size="sm"
            variant="outline"
            className="gap-1.5"
            onClick={() => {
              setShowEsxiForm(true);
            }}
          >
            <Plus />
            Add ESXi source
          </Button>
        ) : undefined
      }
    >
      <div ref={section} tabIndex={-1} className="space-y-3 outline-hidden">
        {showEsxiForm && (
          // The form draws its own frame for the import dialog; inside this
          // panel that would be a box in a box, so it is taken off here.
          <div className="[&>div]:rounded-none [&>div]:border-0 [&>div]:p-0">
            <EsxiSourceForm
              clusterId={clusterId}
              onRegistered={() => {
                setShowEsxiForm(false);
              }}
              onCancel={() => {
                setShowEsxiForm(false);
              }}
            />
          </div>
        )}

        {groups.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            {/* Each step is offered to a role that may take it. */}
            No import sources.
            {canManageImport && " Add an ESXi/vCenter host."}
            {canManageStorage &&
              (canManageImport
                ? ' Or enable the "import" content type on a directory/NFS storage.'
                : ' Enable the "import" content type on a directory/NFS storage.')}
          </p>
        ) : (
          <ul
            className={cn("divide-y text-sm", showEsxiForm && "border-t pt-3")}
          >
            {groups.map((g) => (
              <li
                key={g.storage}
                className="flex items-center gap-3 py-2 first:pt-0 last:pb-0"
              >
                <div className="min-w-0 flex-1">
                  <div className="truncate font-medium" title={g.storage}>
                    {g.storage}
                  </div>
                  <div className="truncate text-[12.5px] text-muted-foreground">
                    {g.type}
                    {", "}
                    {g.type === "esxi"
                      ? "ESXi source"
                      : g.shared
                        ? "shared"
                        : g.nodes.join(", ")}
                  </div>
                </div>
                {canManageImport && (g.removesStore || canManageStorage) && (
                  <Button
                    aria-label={
                      g.removesStore
                        ? `Delete ${g.storage}`
                        : `Remove import from ${g.storage}`
                    }
                    variant="ghost"
                    size="sm"
                    className="h-7 w-7 shrink-0 p-0 text-muted-foreground hover:text-destructive"
                    onClick={() => {
                      // Not the failure of another source's remove.
                      if (!deleteMutation.isPending) deleteMutation.reset();
                      setDeleteTarget(g.storage);
                    }}
                  >
                    <Trash2 />
                  </Button>
                )}
              </li>
            ))}
          </ul>
        )}
      </div>

      <Dialog
        open={deleteTarget !== null}
        onOpenChange={(o) => {
          if (!o) setDeleteTarget(null);
        }}
      >
        <DialogContent
          className="max-w-md"
          fallbackFocus={() => section.current}
        >
          <DialogHeader>
            <DialogTitle>Remove import source?</DialogTitle>
            <DialogDescription>
              {target?.removesStore === false ? (
                <>
                  <span className="font-mono">{deleteTarget}</span> holds other
                  content as well, so it stays: this only takes the import
                  content type off it. Nothing stored on it is touched.
                </>
              ) : (
                <>
                  This removes the storage definition{" "}
                  <span className="font-mono">{deleteTarget}</span> from
                  Proxmox. Guests already imported from it are unaffected.
                </>
              )}
            </DialogDescription>
          </DialogHeader>
          {deleteMutation.isError && (
            <p className="text-sm text-destructive">
              {deleteMutation.error instanceof Error
                ? deleteMutation.error.message
                : "Delete failed"}
            </p>
          )}
          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              onClick={() => {
                setDeleteTarget(null);
              }}
            >
              Cancel
            </Button>
            <Button
              type="button"
              variant="destructive"
              disabled={deleteMutation.isPending}
              onClick={confirmDelete}
            >
              {deleteMutation.isPending ? "Removing…" : "Remove source"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </Panel>
  );
}
