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
import { EsxiSourceForm } from "./EsxiSourceForm";
import type { ImportSource } from "@/types/api";

interface ImportSourcesManagerProps {
  clusterId: string;
}

interface SourceGroup {
  storage: string;
  type: string;
  shared: boolean;
  nodes: string[];
}

export function ImportSourcesManager({ clusterId }: ImportSourcesManagerProps) {
  const { data: sources } = useImportSources(clusterId);
  const { canManage } = usePermissions();
  const deleteMutation = useDeleteImportSource();
  const [showEsxiForm, setShowEsxiForm] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState<string | null>(null);
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
        });
      }
    });
    return [...byStorage.values()].sort((a, b) =>
      a.storage.localeCompare(b.storage),
    );
  }, [sources]);

  const canManageImport = canManage("vm_import");

  function confirmDelete() {
    if (!deleteTarget) return;
    deleteMutation.mutate(
      { clusterId, storage: deleteTarget },
      {
        onSettled: () => {
          setDeleteTarget(null);
        },
      },
    );
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
            No import sources. Add an ESXi/vCenter host, or enable the
            &quot;import&quot; content type on a directory/NFS storage.
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
                {canManageImport && (
                  <Button
                    aria-label={`Delete ${g.storage}`}
                    variant="ghost"
                    size="sm"
                    className="h-7 w-7 shrink-0 p-0 text-muted-foreground hover:text-destructive"
                    onClick={() => {
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
              This removes the storage definition{" "}
              <span className="font-mono">{deleteTarget}</span> from Proxmox.
              Guests already imported from it are unaffected.
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
