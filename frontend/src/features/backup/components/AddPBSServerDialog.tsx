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
import { useCreatePBSServer } from "../api/backup-queries";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";

interface AddPBSServerDialogProps {
  trigger?: React.ReactNode;
}

export function AddPBSServerDialog({ trigger }: AddPBSServerDialogProps) {
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [apiUrl, setApiUrl] = useState("");
  const [tokenId, setTokenId] = useState("");
  const [tokenSecret, setTokenSecret] = useState("");
  const [tlsFingerprint, setTlsFingerprint] = useState("");
  const [clusterId, setClusterId] = useState("");
  const createPBS = useCreatePBSServer();
  const clustersQuery = useClusters();
  const clusters = clustersQuery.data ?? [];

  function resetForm() {
    setName("");
    setApiUrl("");
    setTokenId("");
    setTokenSecret("");
    setTlsFingerprint("");
    setClusterId("");
    createPBS.reset();
  }

  function handleOpenChange(nextOpen: boolean) {
    setOpen(nextOpen);
    if (!nextOpen) {
      resetForm();
    }
  }

  function handleSubmit(e: React.SyntheticEvent<HTMLFormElement>) {
    e.preventDefault();
    createPBS.mutate(
      {
        name,
        api_url: apiUrl,
        token_id: tokenId,
        token_secret: tokenSecret,
        tls_fingerprint: tlsFingerprint,
        cluster_id: clusterId || null,
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
      Add PBS Server
    </Button>
  );

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogTrigger asChild>{trigger ?? defaultTrigger}</DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Add PBS Server</DialogTitle>
          <DialogDescription>
            Connect a Proxmox Backup Server by providing its API URL and an API
            token.
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={handleSubmit} className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="pbs-name">Server Name</Label>
            <Input
              id="pbs-name"
              placeholder="Backup Server 1"
              value={name}
              onChange={(e) => {
                setName(e.target.value);
              }}
              required
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="pbs-api-url">API URL</Label>
            <Input
              id="pbs-api-url"
              placeholder="https://pbs.example.com:8007"
              value={apiUrl}
              onChange={(e) => {
                setApiUrl(e.target.value);
              }}
              required
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="pbs-token-id">API Token ID</Label>
            <Input
              id="pbs-token-id"
              placeholder="root@pam!proxdash"
              value={tokenId}
              onChange={(e) => {
                setTokenId(e.target.value);
              }}
              required
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="pbs-token-secret">API Token Secret</Label>
            <Input
              id="pbs-token-secret"
              type="password"
              placeholder="xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"
              value={tokenSecret}
              onChange={(e) => {
                setTokenSecret(e.target.value);
              }}
              required
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="pbs-tls-fingerprint">TLS Fingerprint (optional)</Label>
            <Input
              id="pbs-tls-fingerprint"
              placeholder="AB:CD:EF:..."
              value={tlsFingerprint}
              onChange={(e) => {
                setTlsFingerprint(e.target.value);
              }}
            />
            <p className="text-xs text-muted-foreground">
              Required for self-signed certificates. SHA-256 fingerprint of the server certificate.
            </p>
          </div>
          <div className="space-y-2">
            <Label htmlFor="pbs-cluster">Associated Cluster (optional)</Label>
            <select
              id="pbs-cluster"
              className="w-full rounded-md border bg-background px-3 py-2 text-sm"
              value={clusterId}
              onChange={(e) => {
                setClusterId(e.target.value);
              }}
            >
              <option value="">None</option>
              {clusters.map((c) => (
                <option key={c.id} value={c.id}>
                  {c.name}
                </option>
              ))}
            </select>
          </div>

          {createPBS.error != null && (
            <p className="text-sm text-destructive">
              {createPBS.error instanceof Error
                ? createPBS.error.message
                : "Failed to add PBS server"}
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
            <Button type="submit" disabled={createPBS.isPending}>
              {createPBS.isPending ? "Adding..." : "Add Server"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
