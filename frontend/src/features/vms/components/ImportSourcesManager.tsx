import { useMemo, useState } from "react";
import { Trash2, Plus } from "lucide-react";
import { Button } from "@/components/ui/button";
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
    <div className="space-y-3">
      <div className="flex items-center justify-between">
        <h2 className="text-sm font-semibold">Import sources</h2>
        {canManageImport && !showEsxiForm && (
          <Button
            type="button"
            size="sm"
            variant="outline"
            onClick={() => {
              setShowEsxiForm(true);
            }}
          >
            <Plus className="mr-1 h-4 w-4" /> Add ESXi source
          </Button>
        )}
      </div>

      {showEsxiForm && (
        <EsxiSourceForm
          clusterId={clusterId}
          onRegistered={() => {
            setShowEsxiForm(false);
          }}
          onCancel={() => {
            setShowEsxiForm(false);
          }}
        />
      )}

      {groups.length === 0 ? (
        <p className="text-sm text-muted-foreground">
          No import sources. Add an ESXi/vCenter host, or enable the
          &quot;import&quot; content type on a directory/NFS storage.
        </p>
      ) : (
        <div className="rounded-md border border-border">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-border text-left text-xs text-muted-foreground">
                <th className="px-3 py-2">Storage</th>
                <th className="px-3 py-2">Type</th>
                <th className="px-3 py-2">Visibility</th>
                <th className="px-3 py-2" />
              </tr>
            </thead>
            <tbody>
              {groups.map((g) => (
                <tr
                  key={g.storage}
                  className="border-b border-border last:border-b-0"
                >
                  <td className="px-3 py-2 font-medium">{g.storage}</td>
                  <td className="px-3 py-2 text-muted-foreground">{g.type}</td>
                  <td className="px-3 py-2 text-muted-foreground">
                    {g.type === "esxi"
                      ? "ESXi source"
                      : g.shared
                        ? "shared"
                        : g.nodes.join(", ")}
                  </td>
                  <td className="px-3 py-2 text-right">
                    {canManageImport && (
                      <Button
                        variant="ghost"
                        size="sm"
                        onClick={() => {
                          setDeleteTarget(g.storage);
                        }}
                      >
                        <Trash2 className="h-4 w-4" />
                      </Button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <Dialog
        open={deleteTarget !== null}
        onOpenChange={(o) => {
          if (!o) setDeleteTarget(null);
        }}
      >
        <DialogContent className="max-w-md">
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
    </div>
  );
}
