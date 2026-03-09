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
import { useCreatePBSServer } from "../api/backup-queries";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import { apiClient } from "@/lib/api-client";

interface FingerprintResponse {
  fingerprint: string;
  self_signed: boolean;
}

interface AddPBSServerDialogProps {
  trigger?: React.ReactNode;
}

export function AddPBSServerDialog({ trigger }: AddPBSServerDialogProps) {
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [apiUrl, setApiUrl] = useState("");
  const [tokenId, setTokenId] = useState("");
  const [tokenSecret, setTokenSecret] = useState("");
  const [clusterId, setClusterId] = useState("");

  // Fingerprint step
  const [fingerprint, setFingerprint] = useState<FingerprintResponse | null>(
    null,
  );
  const [fingerprintAccepted, setFingerprintAccepted] = useState(false);
  const [fetchingFingerprint, setFetchingFingerprint] = useState(false);
  const [fingerprintError, setFingerprintError] = useState<string | null>(
    null,
  );

  const createPBS = useCreatePBSServer();
  const clustersQuery = useClusters();
  const clusters = clustersQuery.data ?? [];

  function resetForm() {
    setName("");
    setApiUrl("");
    setTokenId("");
    setTokenSecret("");
    setClusterId("");
    setFingerprint(null);
    setFingerprintAccepted(false);
    setFetchingFingerprint(false);
    setFingerprintError(null);
    createPBS.reset();
  }

  function handleOpenChange(nextOpen: boolean) {
    setOpen(nextOpen);
    if (!nextOpen) {
      resetForm();
    }
  }

  async function handleFetchFingerprint(
    e: React.SyntheticEvent<HTMLFormElement>,
  ) {
    e.preventDefault();
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
        err instanceof Error
          ? err.message
          : "Failed to fetch TLS certificate",
      );
    } finally {
      setFetchingFingerprint(false);
    }
  }

  function handleCreate() {
    createPBS.mutate(
      {
        name,
        api_url: apiUrl,
        token_id: tokenId,
        token_secret: tokenSecret,
        tls_fingerprint: fingerprint?.fingerprint ?? "",
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

  function handleBack() {
    setFingerprint(null);
    setFingerprintAccepted(false);
    setFingerprintError(null);
  }

  const defaultTrigger = (
    <Button>
      <Plus className="mr-2 h-4 w-4" />
      Add PBS Server
    </Button>
  );

  const showFingerprint = fingerprint !== null;

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogTrigger asChild>{trigger ?? defaultTrigger}</DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Add PBS Server</DialogTitle>
          <DialogDescription>
            {showFingerprint
              ? "Verify the TLS certificate fingerprint before connecting."
              : "Connect a Proxmox Backup Server by providing its API URL and an API token."}
          </DialogDescription>
        </DialogHeader>

        {!showFingerprint ? (
          <form onSubmit={(e) => { void handleFetchFingerprint(e); }} className="space-y-4">
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
                placeholder="root@pam!nexara"
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

            {fingerprintError != null && (
              <p className="text-sm text-destructive">{fingerprintError}</p>
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
              <Button type="submit" disabled={fetchingFingerprint}>
                {fetchingFingerprint ? "Connecting..." : "Connect"}
              </Button>
            </DialogFooter>
          </form>
        ) : (
          <div className="space-y-4">
            {fingerprint.self_signed ? (
              <div className="space-y-3 rounded-lg border border-yellow-500/50 bg-yellow-500/10 p-4">
                <div className="flex items-center gap-2 text-yellow-600 dark:text-yellow-500">
                  <ShieldAlert className="h-5 w-5 shrink-0" />
                  <span className="font-medium">Self-Signed Certificate</span>
                </div>
                <p className="text-sm text-muted-foreground">
                  The server at <strong>{apiUrl}</strong> uses a self-signed
                  certificate. Verify this fingerprint matches your PBS host
                  before accepting.
                </p>
                <div className="rounded-md bg-muted p-3">
                  <p className="mb-1 text-xs text-muted-foreground">
                    SHA-256 Fingerprint
                  </p>
                  <code className="select-all break-all font-mono text-xs">
                    {fingerprint.fingerprint}
                  </code>
                </div>
                <div className="flex items-center gap-2">
                  <Checkbox
                    id="accept-pbs-fingerprint"
                    checked={fingerprintAccepted}
                    onCheckedChange={(checked) => {
                      setFingerprintAccepted(Boolean(checked));
                    }}
                  />
                  <Label htmlFor="accept-pbs-fingerprint" className="text-sm">
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
                <p className="mt-1 text-sm text-muted-foreground">
                  The server at <strong>{apiUrl}</strong> has a valid certificate
                  signed by a trusted CA.
                </p>
              </div>
            )}

            {createPBS.error != null && (
              <p className="text-sm text-destructive">
                {createPBS.error instanceof Error
                  ? createPBS.error.message
                  : "Failed to add PBS server"}
              </p>
            )}

            <DialogFooter>
              <Button type="button" variant="outline" onClick={handleBack}>
                Back
              </Button>
              <Button
                onClick={handleCreate}
                disabled={!fingerprintAccepted || createPBS.isPending}
              >
                {createPBS.isPending ? "Adding..." : "Add Server"}
              </Button>
            </DialogFooter>
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}
