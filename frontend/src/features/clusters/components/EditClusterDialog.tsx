import { useState, useEffect } from "react";
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
import {
  ShieldAlert,
  ShieldCheck,
  RefreshCw,
  AlertTriangle,
} from "lucide-react";
import { apiClient } from "@/lib/api-client";
import {
  privateAddressWarningFromError,
  type PrivateAddressWarning as PrivateAddressDetails,
} from "@/lib/private-address";
import { PrivateAddressWarning } from "@/components/PrivateAddressWarning";
import { ConfirmRequiredWarning } from "@/components/ConfirmRequiredWarning";
import {
  confirmRequiredFromError,
  type ConfirmRequired,
} from "@/lib/confirm-gate";
import { useSSHCredentials } from "@/features/rolling-updates/api/rolling-update-queries";

/** The backend's code for the SSH trust reset (clusters.go). */
const SSH_TRUST_RESET = "ssh_trust_reset_confirm_required";
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

export function EditClusterDialog({
  cluster,
  open,
  onOpenChange,
}: EditClusterDialogProps) {
  const [name, setName] = useState(cluster.name);
  const [apiUrl, setApiUrl] = useState(cluster.api_url);
  const [tokenId, setTokenId] = useState(cluster.token_id);
  const [tokenSecret, setTokenSecret] = useState("");

  // Fingerprint state
  const [fingerprint, setFingerprint] = useState<FingerprintResponse | null>(
    null,
  );
  const [fingerprintAccepted, setFingerprintAccepted] = useState(false);
  const [fetchingFingerprint, setFetchingFingerprint] = useState(false);
  const [fingerprintError, setFingerprintError] = useState<string | null>(null);

  // SSRF policy gate.
  const [privateWarning, setPrivateWarning] =
    useState<PrivateAddressDetails | null>(null);
  const [privateWarningSource, setPrivateWarningSource] = useState<
    "fetch" | "update" | null
  >(null);
  const [allowPrivate, setAllowPrivate] = useState(false);

  const updateMutation = useUpdateCluster();

  // The stored token secret is only ever sent to the address it was saved for,
  // so moving the address means re-entering it. The backend refuses the
  // combination outright (ClusterHandler.Update, which dials the new address
  // with the stored secret if it gets that far); this makes the field required
  // before the request is worth sending, and says why.
  const addressChanged = apiUrl !== cluster.api_url;

  // Moving the address resets the cluster's SSH trust anchor server-side: node
  // addresses are learned from this API, so a stored SSH credential and its
  // pinned host keys no longer describe the machines Nexara would reach.
  //
  // The server refuses with a 422 until the reset is acknowledged, and THAT is
  // what drives the confirmation below — not this query. The query needs
  // manage:ssh_credentials, which a caller holding only manage:cluster does not
  // have; driving the prompt from it would leave that caller staring at a
  // refusal with no control to satisfy it, unable to change the address at all.
  // It is used only for the advance notice, which is a nicety for callers who
  // can read it.
  const { data: sshCreds } = useSSHCredentials(cluster.id);
  const sshResetForeseen = addressChanged && sshCreds != null;

  // The server's refusal, once it arrives. Re-submitting with the
  // acknowledgement is the only way past it, for every caller.
  const [sshResetConfirm, setSshResetConfirm] =
    useState<ConfirmRequired | null>(null);

  // Radix only calls the Dialog's onOpenChange for its OWN close affordances
  // (Escape, overlay, the built-in X). The footer Cancel calls onOpenChange
  // directly, so the reset has to key on `open` instead — otherwise a typed
  // token secret, an open confirm and an accepted fingerprint all survive a
  // cancel into the next time the dialog is opened.
  useEffect(() => {
    if (!open) {
      resetFingerprintState();
      updateMutation.reset();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  function resetFingerprintState() {
    setFingerprint(null);
    setFingerprintAccepted(false);
    setFetchingFingerprint(false);
    setFingerprintError(null);
    setPrivateWarning(null);
    setPrivateWarningSource(null);
    setAllowPrivate(false);
    setSshResetConfirm(null);
  }

  async function fetchFingerprint(allow: boolean) {
    setFingerprintError(null);
    setPrivateWarning(null);
    setPrivateWarningSource(null);
    setFetchingFingerprint(true);
    try {
      const resp = await apiClient.post<FingerprintResponse>(
        "/api/v1/clusters/fetch-fingerprint",
        { api_url: apiUrl, allow_private_address: allow },
      );
      setFingerprint(resp);
      if (!resp.self_signed) {
        setFingerprintAccepted(true);
      }
    } catch (err) {
      const warn = privateAddressWarningFromError(err);
      if (warn != null) {
        setPrivateWarning(warn);
        setPrivateWarningSource("fetch");
      } else {
        setFingerprintError(
          err instanceof Error
            ? err.message
            : "Failed to fetch TLS certificate",
        );
      }
    } finally {
      setFetchingFingerprint(false);
    }
  }

  function handleFetchFingerprint() {
    void fetchFingerprint(allowPrivate);
  }

  function handleConfirmPrivate() {
    setAllowPrivate(true);
    if (privateWarningSource === "update") {
      submitUpdate(true, sshResetConfirm != null);
    } else {
      void fetchFingerprint(true);
    }
  }

  function handleSubmit(e: React.SyntheticEvent<HTMLFormElement>) {
    e.preventDefault();
    submitUpdate(allowPrivate, false);
  }

  function submitUpdate(allow: boolean, acknowledgeSSHTrustReset: boolean) {
    const body: {
      name?: string;
      api_url?: string;
      token_id?: string;
      token_secret?: string;
      tls_fingerprint?: string;
      allow_private_address?: boolean;
      acknowledge_ssh_trust_reset?: boolean;
    } = {};
    if (name !== cluster.name) body.name = name;
    if (apiUrl !== cluster.api_url) body.api_url = apiUrl;
    if (tokenId !== cluster.token_id) body.token_id = tokenId;
    if (tokenSecret) body.token_secret = tokenSecret;
    if (fingerprint && fingerprintAccepted) {
      body.tls_fingerprint = fingerprint.fingerprint;
    }
    if (allow) {
      body.allow_private_address = true;
    }
    // Only ever sent after the operator has confirmed the server's refusal.
    // Attaching it pre-emptively would make the gate decorative.
    if (acknowledgeSSHTrustReset) {
      body.acknowledge_ssh_trust_reset = true;
    }

    if (Object.keys(body).length === 0) {
      onOpenChange(false);
      return;
    }

    setPrivateWarning(null);
    setPrivateWarningSource(null);
    if (!acknowledgeSSHTrustReset) {
      setSshResetConfirm(null);
    }

    updateMutation.mutate(
      { id: cluster.id, body },
      {
        onSuccess: () => {
          onOpenChange(false);
          resetFingerprintState();
        },
        onError: (err) => {
          const warn = privateAddressWarningFromError(err);
          if (warn != null) {
            setPrivateWarning(warn);
            setPrivateWarningSource("update");
            return;
          }
          // The dialog only ever edits this one cluster, so the target is
          // fixed and a late-settling mutation cannot land on another.
          const confirm = confirmRequiredFromError(
            err,
            [SSH_TRUST_RESET],
            cluster.id,
          );
          if (confirm != null) {
            setSshResetConfirm(confirm);
            return;
          }
          // Anything else must reach the banner below, which is gated on this
          // being null. Leaving a stale confirm mounted hides the failure —
          // and on the acknowledged re-submit that failure can be a 500 AFTER
          // the SSH credential was already deleted, which is the one outcome
          // the operator most needs told about.
          setSshResetConfirm(null);
        },
      },
    );
  }

  const connectivityData = updateMutation.data?.connectivity;
  const showConnectivityWarning =
    connectivityData != null && !connectivityData.reachable;

  return (
    <Dialog
      open={open}
      onOpenChange={(nextOpen) => {
        onOpenChange(nextOpen);
        if (!nextOpen) {
          resetFingerprintState();
          updateMutation.reset();
        }
      }}
    >
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
            <Input
              id="edit-name"
              value={name}
              onChange={(e) => {
                setName(e.target.value);
              }}
              required
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="edit-url">API URL</Label>
            <Input
              id="edit-url"
              value={apiUrl}
              onChange={(e) => {
                setApiUrl(e.target.value);
              }}
              required
            />
            {addressChanged && (
              <p className="text-xs text-muted-foreground">
                This cluster is moving to a new address, so it needs the token
                secret again — the stored one is only ever sent to the address
                it was saved for.
              </p>
            )}
          </div>

          {sshResetForeseen && sshResetConfirm == null && (
            <div className="rounded-lg border border-amber-500/50 bg-amber-500/10 p-3">
              <div className="flex items-start gap-2">
                <AlertTriangle className="h-4 w-4 mt-0.5 text-amber-600 dark:text-amber-500 shrink-0" />
                <div className="space-y-1">
                  <p className="text-sm font-medium text-amber-600 dark:text-amber-500">
                    SSH credential will be cleared
                  </p>
                  <p className="text-sm text-muted-foreground">
                    Node addresses are learned from this API, so the stored SSH
                    credential (<strong>{sshCreds.username}</strong>) and every
                    pinned host key were entrusted to machines this cluster is
                    leaving. Saving clears both. Re-enter the credential and
                    re-pin each node before the next rolling update.
                  </p>
                </div>
              </div>
            </div>
          )}
          <div className="space-y-2">
            <Label htmlFor="edit-token">Token ID</Label>
            <Input
              id="edit-token"
              value={tokenId}
              onChange={(e) => {
                setTokenId(e.target.value);
              }}
              required
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="edit-secret">
              {addressChanged
                ? "Token Secret (required — the address changed)"
                : "Token Secret (leave blank to keep current)"}
            </Label>
            <Input
              id="edit-secret"
              type="password"
              value={tokenSecret}
              onChange={(e) => {
                setTokenSecret(e.target.value);
              }}
              placeholder={
                addressChanged
                  ? "Re-enter the token secret for the new address"
                  : "Unchanged"
              }
              required={addressChanged}
            />
          </div>

          {/* TLS Certificate Section */}
          <div className="space-y-2">
            <div className="flex items-center justify-between">
              <Label>TLS Certificate</Label>
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={handleFetchFingerprint}
                disabled={fetchingFingerprint || !apiUrl}
              >
                <RefreshCw
                  className={`mr-1.5 h-3.5 w-3.5 ${fetchingFingerprint ? "animate-spin" : ""}`}
                />
                {fetchingFingerprint ? "Fetching..." : "Re-fetch Certificate"}
              </Button>
            </div>

            {cluster.tls_fingerprint && !fingerprint && (
              <div className="rounded-md bg-muted p-3">
                <p className="text-xs text-muted-foreground mb-1">
                  Current SHA-256 Fingerprint
                </p>
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
                  <div className="rounded-lg border border-amber-500/50 bg-amber-500/10 p-4 space-y-3">
                    <div className="flex items-center gap-2 text-amber-600 dark:text-amber-500">
                      <ShieldAlert className="h-5 w-5 shrink-0" />
                      <span className="font-medium">
                        Self-Signed Certificate
                      </span>
                    </div>
                    <p className="text-sm text-muted-foreground">
                      The server at <strong>{apiUrl}</strong> uses a self-signed
                      certificate. Verify this fingerprint matches your Proxmox
                      host before accepting.
                    </p>
                    <div className="rounded-md bg-muted p-3">
                      <p className="text-xs text-muted-foreground mb-1">
                        New SHA-256 Fingerprint
                      </p>
                      <code className="text-xs font-mono break-all select-all">
                        {fingerprint.fingerprint}
                      </code>
                    </div>
                    {cluster.tls_fingerprint &&
                      fingerprint.fingerprint !== cluster.tls_fingerprint && (
                        <div className="rounded-md border border-orange-500/50 bg-orange-500/10 p-2">
                          <p className="text-xs text-orange-600 dark:text-orange-400 font-medium">
                            This fingerprint differs from the currently stored
                            fingerprint. The server certificate has changed.
                          </p>
                        </div>
                      )}
                    <div className="flex items-center gap-2">
                      <Checkbox
                        id="accept-fingerprint"
                        checked={fingerprintAccepted}
                        onCheckedChange={(checked) => {
                          setFingerprintAccepted(Boolean(checked));
                        }}
                      />
                      <Label htmlFor="accept-fingerprint" className="text-sm">
                        I have verified this fingerprint and trust this
                        certificate
                      </Label>
                    </div>
                  </div>
                ) : (
                  <div className="rounded-lg border border-emerald-500/50 bg-emerald-500/10 p-4">
                    <div className="flex items-center gap-2 text-emerald-600 dark:text-emerald-500">
                      <ShieldCheck className="h-5 w-5 shrink-0" />
                      <span className="font-medium">Trusted Certificate</span>
                    </div>
                    <p className="text-sm text-muted-foreground mt-1">
                      The server at <strong>{apiUrl}</strong> has a valid
                      certificate signed by a trusted CA.
                    </p>
                    {cluster.tls_fingerprint &&
                      fingerprint.fingerprint !== cluster.tls_fingerprint && (
                        <p className="text-sm text-muted-foreground mt-2">
                          The fingerprint will be updated on save.
                        </p>
                      )}
                  </div>
                )}
              </>
            )}
          </div>

          {sshResetConfirm != null && sshResetConfirm.target === cluster.id && (
            <ConfirmRequiredWarning
              title="SSH credential will be cleared"
              message={sshResetConfirm.message}
              confirmLabel="Clear SSH trust and save"
              onConfirm={() => {
                submitUpdate(allowPrivate, true);
              }}
              onCancel={() => {
                setSshResetConfirm(null);
              }}
              pending={updateMutation.isPending}
            />
          )}

          {privateWarning != null && (
            <PrivateAddressWarning
              ip={privateWarning.ip}
              url={apiUrl}
              onConfirm={handleConfirmPrivate}
              onCancel={() => {
                setPrivateWarning(null);
                setPrivateWarningSource(null);
              }}
              pending={updateMutation.isPending || fetchingFingerprint}
            />
          )}

          {privateWarning == null &&
            sshResetConfirm == null &&
            updateMutation.isError && (
              <p className="text-sm text-destructive">
                {updateMutation.error instanceof Error
                  ? updateMutation.error.message
                  : "Update failed"}
              </p>
            )}

          {showConnectivityWarning && (
            <div className="rounded-lg border border-amber-500/50 bg-amber-500/10 p-3">
              <div className="flex items-start gap-2">
                <AlertTriangle className="h-4 w-4 mt-0.5 text-amber-600 dark:text-amber-500 shrink-0" />
                <div>
                  <p className="text-sm font-medium text-amber-600 dark:text-amber-500">
                    Connectivity Issue
                  </p>
                  <p className="text-sm text-muted-foreground">
                    {connectivityData.message}
                  </p>
                </div>
              </div>
            </div>
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
            <Button
              type="submit"
              disabled={
                updateMutation.isPending ||
                (fingerprint != null &&
                  fingerprint.self_signed &&
                  !fingerprintAccepted)
              }
            >
              {updateMutation.isPending ? "Saving..." : "Save"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
