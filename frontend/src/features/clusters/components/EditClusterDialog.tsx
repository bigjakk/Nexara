import { useState } from "react";
import { useUpdateCluster } from "@/features/dashboard/api/dashboard-queries";
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
} from "@/components/ui/dialog";
import { ShieldAlert, ShieldCheck, RefreshCw, AlertTriangle } from "lucide-react";
import { apiClient } from "@/lib/api-client";
import type { ClusterResponse } from "@/types/api";

interface FingerprintResponse {
  fingerprint: string;
  self_signed: boolean;
}

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

  // Fingerprint state
  const [fingerprint, setFingerprint] = useState<FingerprintResponse | null>(null);
  const [fingerprintAccepted, setFingerprintAccepted] = useState(false);
  const [fetchingFingerprint, setFetchingFingerprint] = useState(false);
  const [fingerprintError, setFingerprintError] = useState<string | null>(null);

  const updateMutation = useUpdateCluster();

  function resetFingerprintState() {
    setFingerprint(null);
    setFingerprintAccepted(false);
    setFetchingFingerprint(false);
    setFingerprintError(null);
  }

  async function handleFetchFingerprint() {
    setFingerprintError(null);
    setFetchingFingerprint(true);
    try {
      const resp = await apiClient.post<FingerprintResponse>(
        "/api/v1/clusters/fetch-fingerprint",
        { api_url: apiUrl },
      );
      setFingerprint(resp);
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

  function handleSubmit(e: React.SyntheticEvent<HTMLFormElement>) {
    e.preventDefault();
    const body: Record<string, string> = {};
    if (name !== cluster.name) body["name"] = name;
    if (apiUrl !== cluster.api_url) body["api_url"] = apiUrl;
    if (tokenId !== cluster.token_id) body["token_id"] = tokenId;
    if (tokenSecret) body["token_secret"] = tokenSecret;
    if (fingerprint && fingerprintAccepted) {
      body["tls_fingerprint"] = fingerprint.fingerprint;
    }

    if (Object.keys(body).length === 0) {
      onOpenChange(false);
      return;
    }

    updateMutation.mutate(
      { id: cluster.id, body },
      {
        onSuccess: () => {
          onOpenChange(false);
          resetFingerprintState();
        },
      },
    );
  }

  const connectivityData = updateMutation.data?.connectivity;
  const showConnectivityWarning = connectivityData != null && !connectivityData.reachable;

  return (
    <Dialog open={open} onOpenChange={(nextOpen) => {
      onOpenChange(nextOpen);
      if (!nextOpen) {
        resetFingerprintState();
        updateMutation.reset();
      }
    }}>
      <DialogContent className="sm:max-w-lg">
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

          {/* TLS Certificate Section */}
          <div className="space-y-2">
            <div className="flex items-center justify-between">
              <Label>TLS Certificate</Label>
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={() => { void handleFetchFingerprint(); }}
                disabled={fetchingFingerprint || !apiUrl}
              >
                <RefreshCw className={`mr-1.5 h-3.5 w-3.5 ${fetchingFingerprint ? "animate-spin" : ""}`} />
                {fetchingFingerprint ? "Fetching..." : "Re-fetch Certificate"}
              </Button>
            </div>

            {cluster.tls_fingerprint && !fingerprint && (
              <div className="rounded-md bg-muted p-3">
                <p className="text-xs text-muted-foreground mb-1">Current SHA-256 Fingerprint</p>
                <code className="text-xs font-mono break-all select-all">
                  {cluster.tls_fingerprint}
                </code>
              </div>
            )}

            {fingerprintError != null && (
              <p className="text-sm text-destructive">{fingerprintError}</p>
            )}

            {fingerprint != null && (
              <>
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
                      <p className="text-xs text-muted-foreground mb-1">New SHA-256 Fingerprint</p>
                      <code className="text-xs font-mono break-all select-all">
                        {fingerprint.fingerprint}
                      </code>
                    </div>
                    {cluster.tls_fingerprint && fingerprint.fingerprint !== cluster.tls_fingerprint && (
                      <div className="rounded-md border border-orange-500/50 bg-orange-500/10 p-2">
                        <p className="text-xs text-orange-600 dark:text-orange-400 font-medium">
                          This fingerprint differs from the currently stored fingerprint. The server certificate has changed.
                        </p>
                      </div>
                    )}
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
                    {cluster.tls_fingerprint && fingerprint.fingerprint !== cluster.tls_fingerprint && (
                      <p className="text-sm text-muted-foreground mt-2">
                        The fingerprint will be updated on save.
                      </p>
                    )}
                  </div>
                )}
              </>
            )}
          </div>

          {updateMutation.isError && (
            <p className="text-sm text-destructive">
              {updateMutation.error instanceof Error ? updateMutation.error.message : "Update failed"}
            </p>
          )}

          {showConnectivityWarning && (
            <div className="rounded-lg border border-yellow-500/50 bg-yellow-500/10 p-3">
              <div className="flex items-start gap-2">
                <AlertTriangle className="h-4 w-4 mt-0.5 text-yellow-600 dark:text-yellow-500 shrink-0" />
                <div>
                  <p className="text-sm font-medium text-yellow-600 dark:text-yellow-500">Connectivity Issue</p>
                  <p className="text-sm text-muted-foreground">
                    {connectivityData.message}
                  </p>
                </div>
              </div>
            </div>
          )}

          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => { onOpenChange(false); }}>Cancel</Button>
            <Button
              type="submit"
              disabled={updateMutation.isPending || (fingerprint != null && fingerprint.self_signed && !fingerprintAccepted)}
            >
              {updateMutation.isPending ? "Saving..." : "Save"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
