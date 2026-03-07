import { useState, useEffect } from "react";
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
import { useUpdatePBSServer } from "../api/backup-queries";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import type { PBSServer } from "../types/backup";

interface EditPBSServerDialogProps {
  server: PBSServer;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

export function EditPBSServerDialog({
  server,
  open,
  onOpenChange,
}: EditPBSServerDialogProps) {
  const [name, setName] = useState(server.name);
  const [apiUrl, setApiUrl] = useState(server.api_url);
  const [tokenId, setTokenId] = useState(server.token_id);
  const [tokenSecret, setTokenSecret] = useState("");
  const [tlsFingerprint, setTlsFingerprint] = useState(
    server.tls_fingerprint,
  );
  const [clusterId, setClusterId] = useState(server.cluster_id ?? "");

  const updatePBS = useUpdatePBSServer();
  const clustersQuery = useClusters();
  const clusters = clustersQuery.data ?? [];

  useEffect(() => {
    if (open) {
      setName(server.name);
      setApiUrl(server.api_url);
      setTokenId(server.token_id);
      setTokenSecret("");
      setTlsFingerprint(server.tls_fingerprint);
      setClusterId(server.cluster_id ?? "");
      updatePBS.reset();
    }
  }, [open, server]);

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault();

    const body: {
      id: string;
      name?: string;
      api_url?: string;
      token_id?: string;
      token_secret?: string;
      tls_fingerprint?: string;
      cluster_id?: string;
    } = { id: server.id };
    if (name !== server.name) body.name = name;
    if (apiUrl !== server.api_url) body.api_url = apiUrl;
    if (tokenId !== server.token_id) body.token_id = tokenId;
    if (tokenSecret.length > 0) body.token_secret = tokenSecret;
    if (tlsFingerprint !== server.tls_fingerprint)
      body.tls_fingerprint = tlsFingerprint;
    if ((clusterId || null) !== (server.cluster_id || null))
      body.cluster_id = clusterId || "";

    updatePBS.mutate(body, {
      onSuccess: () => {
        onOpenChange(false);
      },
    });
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Edit PBS Server</DialogTitle>
          <DialogDescription>
            Update the connection settings for{" "}
            <strong>{server.name}</strong>.
          </DialogDescription>
        </DialogHeader>

        <form onSubmit={handleSubmit} className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="edit-pbs-name">Server Name</Label>
            <Input
              id="edit-pbs-name"
              value={name}
              onChange={(e) => {
                setName(e.target.value);
              }}
              required
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="edit-pbs-api-url">API URL</Label>
            <Input
              id="edit-pbs-api-url"
              value={apiUrl}
              onChange={(e) => {
                setApiUrl(e.target.value);
              }}
              required
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="edit-pbs-token-id">API Token ID</Label>
            <Input
              id="edit-pbs-token-id"
              value={tokenId}
              onChange={(e) => {
                setTokenId(e.target.value);
              }}
              required
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="edit-pbs-token-secret">
              API Token Secret{" "}
              <span className="text-muted-foreground font-normal">
                (leave blank to keep current)
              </span>
            </Label>
            <Input
              id="edit-pbs-token-secret"
              type="password"
              placeholder="Leave blank to keep current secret"
              value={tokenSecret}
              onChange={(e) => {
                setTokenSecret(e.target.value);
              }}
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="edit-pbs-fingerprint">TLS Fingerprint</Label>
            <Input
              id="edit-pbs-fingerprint"
              value={tlsFingerprint}
              onChange={(e) => {
                setTlsFingerprint(e.target.value);
              }}
              placeholder="SHA-256 fingerprint"
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="edit-pbs-cluster">
              Associated Cluster
              <span className="ml-1 text-xs font-normal text-muted-foreground">
                (used to scope backup coverage by cluster)
              </span>
            </Label>
            <select
              id="edit-pbs-cluster"
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

          {updatePBS.isError && (
            <p className="text-sm text-destructive">
              {updatePBS.error instanceof Error
                ? updatePBS.error.message
                : "Failed to update PBS server"}
            </p>
          )}

          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              onClick={() => {
                onOpenChange(false);
              }}
            >
              Cancel
            </Button>
            <Button type="submit" disabled={updatePBS.isPending}>
              {updatePBS.isPending ? "Saving..." : "Save Changes"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
