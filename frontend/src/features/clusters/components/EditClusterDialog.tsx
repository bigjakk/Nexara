import { useState } from "react";
import { useUpdateCluster } from "@/features/dashboard/api/dashboard-queries";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import type { ClusterResponse } from "@/types/api";

interface EditClusterDialogProps {
  cluster: ClusterResponse;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

export function EditClusterDialog({ cluster, open, onOpenChange }: EditClusterDialogProps) {
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
