import { useState } from "react";
import { Link } from "react-router-dom";
import { useQueries } from "@tanstack/react-query";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import { useUpdateCluster, useDeleteCluster } from "@/features/dashboard/api/dashboard-queries";
import { AddClusterDialog } from "@/features/dashboard/components/AddClusterDialog";
import { apiClient } from "@/lib/api-client";
import type { NodeResponse, VMResponse } from "@/types/api";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Skeleton } from "@/components/ui/skeleton";
import { Pencil, Trash2 } from "lucide-react";
import type { ClusterResponse } from "@/types/api";

interface EditClusterDialogProps {
  cluster: ClusterResponse;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

function EditClusterDialog({ cluster, open, onOpenChange }: EditClusterDialogProps) {
  const [name, setName] = useState(cluster.name);
  const [apiUrl, setApiUrl] = useState(cluster.api_url);
  const [tokenId, setTokenId] = useState(cluster.token_id);
  const [tokenSecret, setTokenSecret] = useState("");
  const updateMutation = useUpdateCluster();

  function handleSubmit(e: React.SyntheticEvent<HTMLFormElement>) {
    e.preventDefault();
    const body: Record<string, string> = {};
    if (name !== cluster.name) body["name"] = name;
    if (apiUrl !== cluster.api_url) body["api_url"] = apiUrl;
    if (tokenId !== cluster.token_id) body["token_id"] = tokenId;
    if (tokenSecret) body["token_secret"] = tokenSecret;

    if (Object.keys(body).length === 0) {
      onOpenChange(false);
      return;
    }

    updateMutation.mutate(
      { id: cluster.id, body },
      {
        onSuccess: () => {
          onOpenChange(false);
        },
      },
    );
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Edit Cluster</DialogTitle>
          <DialogDescription>
            Update the configuration for {cluster.name}.
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={handleSubmit} className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="edit-name">Name</Label>
            <Input id="edit-name" value={name} onChange={(e) => { setName(e.target.value); }} required />
          </div>
          <div className="space-y-2">
            <Label htmlFor="edit-url">API URL</Label>
            <Input id="edit-url" value={apiUrl} onChange={(e) => { setApiUrl(e.target.value); }} required />
          </div>
          <div className="space-y-2">
            <Label htmlFor="edit-token">Token ID</Label>
            <Input id="edit-token" value={tokenId} onChange={(e) => { setTokenId(e.target.value); }} required />
          </div>
          <div className="space-y-2">
            <Label htmlFor="edit-secret">Token Secret (leave blank to keep current)</Label>
            <Input id="edit-secret" type="password" value={tokenSecret} onChange={(e) => { setTokenSecret(e.target.value); }} placeholder="Unchanged" />
          </div>
          {updateMutation.isError && (
            <p className="text-sm text-destructive">
              {updateMutation.error instanceof Error ? updateMutation.error.message : "Update failed"}
            </p>
          )}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => { onOpenChange(false); }}>Cancel</Button>
            <Button type="submit" disabled={updateMutation.isPending}>
              {updateMutation.isPending ? "Saving..." : "Save"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

interface DeleteClusterDialogProps {
  cluster: ClusterResponse;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

function DeleteClusterDialog({ cluster, open, onOpenChange }: DeleteClusterDialogProps) {
  const [confirmName, setConfirmName] = useState("");
  const deleteMutation = useDeleteCluster();

  function handleDelete() {
    deleteMutation.mutate(cluster.id, {
      onSuccess: () => {
        onOpenChange(false);
      },
    });
  }

  return (
    <Dialog open={open} onOpenChange={(v) => { onOpenChange(v); if (!v) setConfirmName(""); }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Delete Cluster</DialogTitle>
          <DialogDescription>
            This will permanently remove <strong>{cluster.name}</strong> and all associated data (nodes, VMs, metrics).
            Type the cluster name to confirm.
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-4 py-2">
          <Input
            placeholder={cluster.name}
            value={confirmName}
            onChange={(e) => { setConfirmName(e.target.value); }}
          />
          {deleteMutation.isError && (
            <p className="text-sm text-destructive">
              {deleteMutation.error instanceof Error ? deleteMutation.error.message : "Delete failed"}
            </p>
          )}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => { onOpenChange(false); }}>Cancel</Button>
          <Button
            variant="destructive"
            disabled={confirmName !== cluster.name || deleteMutation.isPending}
            onClick={handleDelete}
          >
            {deleteMutation.isPending ? "Deleting..." : "Delete Cluster"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

export function ClustersListPage() {
  const { data: clusters, isLoading, error } = useClusters();
  const [editCluster, setEditCluster] = useState<ClusterResponse | null>(null);
  const [deleteCluster, setDeleteCluster] = useState<ClusterResponse | null>(null);

  const nodeQueries = useQueries({
    queries: (clusters ?? []).map((cluster) => ({
      queryKey: ["clusters", cluster.id, "nodes"],
      queryFn: () =>
        apiClient.get<NodeResponse[]>(`/api/v1/clusters/${cluster.id}/nodes`),
      enabled: clusters !== undefined && clusters.length > 0,
    })),
  });

  const vmQueries = useQueries({
    queries: (clusters ?? []).map((cluster) => ({
      queryKey: ["clusters", cluster.id, "vms"],
      queryFn: () =>
        apiClient.get<VMResponse[]>(`/api/v1/clusters/${cluster.id}/vms`),
      enabled: clusters !== undefined && clusters.length > 0,
    })),
  });

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-bold">Clusters</h1>
        <AddClusterDialog />
      </div>

      {error != null && (
        <div className="rounded-lg border border-destructive bg-destructive/10 p-4 text-destructive">
          Failed to load clusters.
        </div>
      )}

      {isLoading && (
        <div className="space-y-2">
          {Array.from({ length: 3 }, (_, i) => (
            <Skeleton key={i} className="h-12 w-full" />
          ))}
        </div>
      )}

      {!isLoading && !error && clusters?.length === 0 && (
        <div
          className="rounded-lg border border-dashed p-8 text-center text-muted-foreground"
          data-testid="empty-state"
        >
          No clusters configured. Add one to get started.
        </div>
      )}

      {!isLoading && clusters !== undefined && clusters.length > 0 && (
        <div className="rounded-lg border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Status</TableHead>
                <TableHead className="text-right">Nodes</TableHead>
                <TableHead className="text-right">VMs</TableHead>
                <TableHead>API URL</TableHead>
                <TableHead className="w-24" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {clusters.map((cluster, i) => {
                const nodes = nodeQueries[i]?.data;
                const vms = vmQueries[i]?.data;
                const vmCount = vms?.filter((v) => v.type === "qemu").length;

                return (
                  <TableRow key={cluster.id}>
                    <TableCell>
                      <Link
                        to={`/clusters/${cluster.id}`}
                        className="font-medium text-primary hover:underline"
                        data-testid={`cluster-link-${cluster.id}`}
                      >
                        {cluster.name}
                      </Link>
                    </TableCell>
                    <TableCell>
                      <Badge variant={cluster.is_active ? "default" : "secondary"}>
                        {cluster.is_active ? "Active" : "Inactive"}
                      </Badge>
                    </TableCell>
                    <TableCell className="text-right">
                      {nodes !== undefined ? nodes.length : "—"}
                    </TableCell>
                    <TableCell className="text-right">
                      {vmCount !== undefined ? vmCount : "—"}
                    </TableCell>
                    <TableCell className="text-muted-foreground">
                      {cluster.api_url}
                    </TableCell>
                    <TableCell>
                      <div className="flex gap-1">
                        <Button
                          variant="ghost"
                          size="icon"
                          className="h-7 w-7"
                          onClick={() => { setEditCluster(cluster); }}
                        >
                          <Pencil className="h-3.5 w-3.5" />
                        </Button>
                        <Button
                          variant="ghost"
                          size="icon"
                          className="h-7 w-7"
                          onClick={() => { setDeleteCluster(cluster); }}
                        >
                          <Trash2 className="h-3.5 w-3.5 text-destructive" />
                        </Button>
                      </div>
                    </TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        </div>
      )}

      {editCluster && (
        <EditClusterDialog
          cluster={editCluster}
          open={true}
          onOpenChange={(v) => { if (!v) setEditCluster(null); }}
        />
      )}
      {deleteCluster && (
        <DeleteClusterDialog
          cluster={deleteCluster}
          open={true}
          onOpenChange={(v) => { if (!v) setDeleteCluster(null); }}
        />
      )}
    </div>
  );
}
