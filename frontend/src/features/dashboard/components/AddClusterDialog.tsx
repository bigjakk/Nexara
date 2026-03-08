import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Plus, ShieldAlert, ShieldCheck } from "lucide-react";
import { useCreateCluster } from "../api/dashboard-queries";
import { apiClient } from "@/lib/api-client";

interface FingerprintResponse {
  fingerprint: string;
  self_signed: boolean;
}

interface AddClusterDialogProps {
  trigger?: React.ReactNode;
}

export function AddClusterDialog({ trigger }: AddClusterDialogProps) {
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [apiUrl, setApiUrl] = useState("");
  const [tokenId, setTokenId] = useState("");
  const [tokenSecret, setTokenSecret] = useState("");

  // Fingerprint step
  const [fingerprint, setFingerprint] = useState<FingerprintResponse | null>(null);
  const [fingerprintAccepted, setFingerprintAccepted] = useState(false);
  const [fetchingFingerprint, setFetchingFingerprint] = useState(false);
  const [fingerprintError, setFingerprintError] = useState<string | null>(null);

  const createCluster = useCreateCluster();

  function resetForm() {
    setName("");
    setApiUrl("");
    setTokenId("");
    setTokenSecret("");
    setFingerprint(null);
    setFingerprintAccepted(false);
    setFetchingFingerprint(false);
    setFingerprintError(null);
    createCluster.reset();
  }

  function handleOpenChange(nextOpen: boolean) {
    setOpen(nextOpen);
    if (!nextOpen) {
      resetForm();
    }
  }

  async function handleFetchFingerprint(e: React.SyntheticEvent<HTMLFormElement>) {
    e.preventDefault();
    setFingerprintError(null);
    setFetchingFingerprint(true);
    try {
      const resp = await apiClient.post<FingerprintResponse>(
        "/api/v1/clusters/fetch-fingerprint",
        { api_url: apiUrl },
      );
      setFingerprint(resp);
      // Auto-accept if the cert is NOT self-signed (trusted CA)
      if (!resp.self_signed) {
        setFingerprintAccepted(true);
      }
    } catch (err) {
      setFingerprintError(
        err instanceof Error ? err.message : "Failed to fetch TLS certificate",
      );
    } finally {
      setFetchingFingerprint(false);
    }
  }

  function handleCreate() {
    const req: Parameters<typeof createCluster.mutate>[0] = {
      name,
      api_url: apiUrl,
      token_id: tokenId,
      token_secret: tokenSecret,
    };
    if (fingerprint) {
      req.tls_fingerprint = fingerprint.fingerprint;
    }
    createCluster.mutate(
      req,
      {
        onSuccess: () => {
          setOpen(false);
          resetForm();
        },
      },
    );
  }

  function handleBack() {
    setFingerprint(null);
    setFingerprintAccepted(false);
    setFingerprintError(null);
  }

  const defaultTrigger = (
    <Button>
      <Plus className="mr-2 h-4 w-4" />
      Add Cluster
    </Button>
  );

  // Step 2: Show fingerprint for acceptance
  const showFingerprint = fingerprint !== null;

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogTrigger asChild>{trigger ?? defaultTrigger}</DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Add Proxmox Cluster</DialogTitle>
          <DialogDescription>
            {showFingerprint
              ? "Verify the TLS certificate fingerprint before connecting."
              : "Connect a Proxmox VE cluster by providing its API URL and an API token."}
          </DialogDescription>
        </DialogHeader>

        {!showFingerprint ? (
          <form onSubmit={(e) => { void handleFetchFingerprint(e); }} className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="cluster-name">Cluster Name</Label>
              <Input
                id="cluster-name"
                placeholder="Production Cluster"
                value={name}
                onChange={(e) => { setName(e.target.value); }}
                required
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="api-url">API URL</Label>
              <Input
                id="api-url"
                placeholder="https://pve.example.com:8006"
                value={apiUrl}
                onChange={(e) => { setApiUrl(e.target.value); }}
                required
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="token-id">API Token ID</Label>
              <Input
                id="token-id"
                placeholder="root@pam!proxdash"
                value={tokenId}
                onChange={(e) => { setTokenId(e.target.value); }}
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
                onChange={(e) => { setTokenSecret(e.target.value); }}
                required
              />
            </div>

            {fingerprintError != null && (
              <p className="text-sm text-destructive">{fingerprintError}</p>
            )}

            <DialogFooter>
              <Button
                type="button"
                variant="outline"
                onClick={() => { handleOpenChange(false); }}
              >
                Cancel
              </Button>
              <Button type="submit" disabled={fetchingFingerprint}>
                {fetchingFingerprint ? "Connecting..." : "Connect"}
              </Button>
            </DialogFooter>
          </form>
        ) : (
          <div className="space-y-4">
            {fingerprint.self_signed ? (
              <div className="rounded-lg border border-yellow-500/50 bg-yellow-500/10 p-4 space-y-3">
                <div className="flex items-center gap-2 text-yellow-600 dark:text-yellow-500">
                  <ShieldAlert className="h-5 w-5 shrink-0" />
                  <span className="font-medium">Self-Signed Certificate</span>
                </div>
                <p className="text-sm text-muted-foreground">
                  The server at <strong>{apiUrl}</strong> uses a self-signed certificate.
                  Verify this fingerprint matches your Proxmox host before accepting.
                </p>
                <div className="rounded-md bg-muted p-3">
                  <p className="text-xs text-muted-foreground mb-1">SHA-256 Fingerprint</p>
                  <code className="text-xs font-mono break-all select-all">
                    {fingerprint.fingerprint}
                  </code>
                </div>
                <div className="flex items-center gap-2">
                  <Checkbox
                    id="accept-fingerprint"
                    checked={fingerprintAccepted}
                    onCheckedChange={(checked) => { setFingerprintAccepted(Boolean(checked)); }}
                  />
                  <Label htmlFor="accept-fingerprint" className="text-sm">
                    I have verified this fingerprint and trust this certificate
                  </Label>
                </div>
              </div>
            ) : (
              <div className="rounded-lg border border-green-500/50 bg-green-500/10 p-4">
                <div className="flex items-center gap-2 text-green-600 dark:text-green-500">
                  <ShieldCheck className="h-5 w-5 shrink-0" />
                  <span className="font-medium">Trusted Certificate</span>
                </div>
                <p className="text-sm text-muted-foreground mt-1">
                  The server at <strong>{apiUrl}</strong> has a valid certificate signed by a trusted CA.
                </p>
              </div>
            )}

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
              <Button type="button" variant="outline" onClick={handleBack}>
                Back
              </Button>
              <Button
                onClick={handleCreate}
                disabled={!fingerprintAccepted || createCluster.isPending}
              >
                {createCluster.isPending ? "Adding..." : "Add Cluster"}
              </Button>
            </DialogFooter>
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}
