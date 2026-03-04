import { useState } from "react";
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
  DialogTrigger,
} from "@/components/ui/dialog";
import { Plus } from "lucide-react";
import { useCreateCluster } from "../api/dashboard-queries";

interface AddClusterDialogProps {
  trigger?: React.ReactNode;
}

export function AddClusterDialog({ trigger }: AddClusterDialogProps) {
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [apiUrl, setApiUrl] = useState("");
  const [tokenId, setTokenId] = useState("");
  const [tokenSecret, setTokenSecret] = useState("");
  const createCluster = useCreateCluster();

  function resetForm() {
    setName("");
    setApiUrl("");
    setTokenId("");
    setTokenSecret("");
    createCluster.reset();
  }

  function handleOpenChange(nextOpen: boolean) {
    setOpen(nextOpen);
    if (!nextOpen) {
      resetForm();
    }
  }

  function handleSubmit(e: React.SyntheticEvent<HTMLFormElement>) {
    e.preventDefault();
    createCluster.mutate(
      {
        name,
        api_url: apiUrl,
        token_id: tokenId,
        token_secret: tokenSecret,
      },
      {
        onSuccess: () => {
          setOpen(false);
          resetForm();
        },
      },
    );
  }

  const defaultTrigger = (
    <Button>
      <Plus className="mr-2 h-4 w-4" />
      Add Cluster
    </Button>
  );

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogTrigger asChild>{trigger ?? defaultTrigger}</DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Add Proxmox Cluster</DialogTitle>
          <DialogDescription>
            Connect a Proxmox VE cluster by providing its API URL and an API
            token.
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={handleSubmit} className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="cluster-name">Cluster Name</Label>
            <Input
              id="cluster-name"
              placeholder="Production Cluster"
              value={name}
              onChange={(e) => {
                setName(e.target.value);
              }}
              required
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="api-url">API URL</Label>
            <Input
              id="api-url"
              placeholder="https://pve.example.com:8006"
              value={apiUrl}
              onChange={(e) => {
                setApiUrl(e.target.value);
              }}
              required
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="token-id">API Token ID</Label>
            <Input
              id="token-id"
              placeholder="root@pam!proxdash"
              value={tokenId}
              onChange={(e) => {
                setTokenId(e.target.value);
              }}
              required
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="token-secret">API Token Secret</Label>
            <Input
              id="token-secret"
              type="password"
              placeholder="xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"
              value={tokenSecret}
              onChange={(e) => {
                setTokenSecret(e.target.value);
              }}
              required
            />
          </div>

          {createCluster.error != null && (
            <p className="text-sm text-destructive">
              {createCluster.error instanceof Error
                ? createCluster.error.message
                : "Failed to create cluster"}
            </p>
          )}

          {createCluster.data?.connectivity != null &&
            !createCluster.data.connectivity.reachable && (
              <p className="text-sm text-yellow-600">
                Cluster created but connectivity check failed:{" "}
                {createCluster.data.connectivity.message}
              </p>
            )}

          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              onClick={() => {
                handleOpenChange(false);
              }}
            >
              Cancel
            </Button>
            <Button type="submit" disabled={createCluster.isPending}>
              {createCluster.isPending ? "Adding..." : "Add Cluster"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
