import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Checkbox } from "@/components/ui/checkbox";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Check, Plus, ShieldAlert, ShieldCheck, Wand2 } from "lucide-react";
import { useCreateCluster } from "../api/dashboard-queries";
import { apiClient, ApiClientError } from "@/lib/api-client";
import {
  privateAddressWarningFromError,
  type PrivateAddressWarning as PrivateAddressDetails,
} from "@/lib/private-address";
import { PrivateAddressWarning } from "@/components/PrivateAddressWarning";
import type {
  BootstrapSummary,
  ConnectivityResult,
  CreateClusterRequest,
} from "@/types/api";

interface FingerprintResponse {
  fingerprint: string;
  self_signed: boolean;
}

interface AddClusterDialogProps {
  trigger?: React.ReactNode;
}

/**
 * How the cluster's API credential is obtained.
 *
 * "bootstrap" is the default: the operator supplies a privileged password once
 * and Nexara mints a dedicated token from it. "token" is the original flow,
 * for anyone who would rather create the token in Proxmox themselves.
 */
type CredentialMode = "bootstrap" | "token";

/**
 * A password typed into a page served over plain HTTP is readable by anything
 * on the wire, and unlike the API token it belongs to a privileged human
 * account that is very likely reused elsewhere. Browsers expose exactly this
 * distinction as isSecureContext (true for https and for localhost).
 *
 * Warn and gate rather than hide the feature outright — a lab reached over a
 * trusted LAN is a legitimate setup, and the operator can still paste a token
 * instead, which is no worse over http than it already was.
 */
function isSecureContext(): boolean {
  return typeof window !== "undefined" && window.isSecureContext;
}

export function AddClusterDialog({ trigger }: AddClusterDialogProps) {
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [apiUrl, setApiUrl] = useState("");
  const [mode, setMode] = useState<CredentialMode>("bootstrap");

  // Paste-a-token mode.
  const [tokenId, setTokenId] = useState("");
  const [tokenSecret, setTokenSecret] = useState("");

  // Let-Nexara-create-one mode.
  const [bootstrapUser, setBootstrapUser] = useState("root@pam");
  const [bootstrapPassword, setBootstrapPassword] = useState("");
  const [otp, setOtp] = useState("");
  const [otpRequired, setOtpRequired] = useState(false);
  const [tokenName, setTokenName] = useState("nexara");
  const [tokenConflict, setTokenConflict] = useState(false);
  const [insecureAccepted, setInsecureAccepted] = useState(false);
  const [bootstrapError, setBootstrapError] = useState<string | null>(null);
  const [summary, setSummary] = useState<BootstrapSummary | null>(null);
  // Held separately because createCluster.reset() clears the mutation result
  // (and with it the cached password) as soon as the summary is shown.
  const [summaryConnectivity, setSummaryConnectivity] =
    useState<ConnectivityResult | null>(null);

  // Fingerprint step
  const [fingerprint, setFingerprint] = useState<FingerprintResponse | null>(null);
  const [fingerprintAccepted, setFingerprintAccepted] = useState(false);
  const [fetchingFingerprint, setFetchingFingerprint] = useState(false);
  const [fingerprintError, setFingerprintError] = useState<string | null>(null);

  // SSRF policy gate — set when the backend returns 422 for a private IP.
  const [privateWarning, setPrivateWarning] =
    useState<PrivateAddressDetails | null>(null);
  const [allowPrivate, setAllowPrivate] = useState(false);

  const createCluster = useCreateCluster();

  const secure = isSecureContext();

  function resetForm() {
    setName("");
    setApiUrl("");
    setMode("bootstrap");
    setTokenId("");
    setTokenSecret("");
    setBootstrapUser("root@pam");
    setBootstrapPassword("");
    setOtp("");
    setOtpRequired(false);
    setTokenName("nexara");
    setTokenConflict(false);
    setInsecureAccepted(false);
    setBootstrapError(null);
    setSummary(null);
    setSummaryConnectivity(null);
    setFingerprint(null);
    setFingerprintAccepted(false);
    setFetchingFingerprint(false);
    setFingerprintError(null);
    setPrivateWarning(null);
    setAllowPrivate(false);
    createCluster.reset();
  }

  function handleOpenChange(nextOpen: boolean) {
    setOpen(nextOpen);
    if (!nextOpen) {
      resetForm();
    }
  }

  async function fetchFingerprint(allow: boolean) {
    setFingerprintError(null);
    setPrivateWarning(null);
    setFetchingFingerprint(true);
    try {
      const resp = await apiClient.post<FingerprintResponse>(
        "/api/v1/clusters/fetch-fingerprint",
        { api_url: apiUrl, allow_private_address: allow },
      );
      setFingerprint(resp);
      // Auto-accept if the cert is NOT self-signed (trusted CA)
      if (!resp.self_signed) {
        setFingerprintAccepted(true);
      }
    } catch (err) {
      const warn = privateAddressWarningFromError(err);
      if (warn != null) {
        setPrivateWarning(warn);
      } else {
        setFingerprintError(
          err instanceof Error ? err.message : "Failed to fetch TLS certificate",
        );
      }
    } finally {
      setFetchingFingerprint(false);
    }
  }

  function handleFetchFingerprintSubmit(
    e: React.SyntheticEvent<HTMLFormElement>,
  ) {
    e.preventDefault();
    void fetchFingerprint(allowPrivate);
  }

  function handleConfirmPrivate() {
    setAllowPrivate(true);
    void fetchFingerprint(true);
  }

  function handleCreate() {
    setBootstrapError(null);
    setTokenConflict(false);

    const req: CreateClusterRequest = { name, api_url: apiUrl };
    if (mode === "bootstrap") {
      req.bootstrap = {
        username: bootstrapUser,
        password: bootstrapPassword,
        token_name: tokenName,
      };
      if (otp !== "") {
        req.bootstrap.otp = otp;
      }
    } else {
      req.token_id = tokenId;
      req.token_secret = tokenSecret;
    }
    if (fingerprint) {
      req.tls_fingerprint = fingerprint.fingerprint;
    }
    if (allowPrivate) {
      req.allow_private_address = true;
    }

    createCluster.mutate(req, {
      onSuccess: (data) => {
        // A bootstrap run has something to show for itself; hold the dialog
        // open on the summary rather than closing over it.
        if (data.bootstrap != null) {
          setSummary(data.bootstrap);
          setBootstrapPassword("");
          setOtp("");
          // The mutation caches its variables, which include the password.
          // Clearing component state is not enough — without this the
          // plaintext lives on in the query cache until the dialog is closed.
          setSummaryConnectivity(data.connectivity);
          createCluster.reset();
          return;
        }
        // Clears the mutation too, so the pasted token secret does not linger
        // in createCluster.variables.
        setOpen(false);
        resetForm();
      },
      onError: (err) => {
        if (!(err instanceof ApiClientError)) {
          return;
        }
        switch (err.body.error) {
          case "tfa_required":
            // Reveal the one-time code field and let them resubmit; the
            // password they already typed is still in the form.
            setOtpRequired(true);
            setBootstrapError(err.body.message);
            break;
          case "token_exists":
            setTokenConflict(true);
            setBootstrapError(err.body.message);
            break;
          default:
            setBootstrapError(err.body.message);
        }
      },
    });
  }

  /**
   * Clear everything that renders as a failed submit.
   *
   * Both `bootstrapError` and `createCluster.error` reach the screen, so
   * resetting only the first makes the same message reappear from the other
   * element — it looks like the error came back on its own.
   */
  function clearSubmitError() {
    setBootstrapError(null);
    setTokenConflict(false);
    createCluster.reset();
  }

  function handleBack() {
    setFingerprint(null);
    setFingerprintAccepted(false);
    setFingerprintError(null);
    // A stale failure from the previous attempt must not still be on screen
    // when the operator comes back to this step.
    clearSubmitError();
  }

  const defaultTrigger = (
    <Button>
      <Plus className="mr-2 h-4 w-4" />
      Add Cluster
    </Button>
  );

  // Step 2: Show fingerprint for acceptance
  const showFingerprint = fingerprint !== null;

  // The password field is gated on a secure context; pasting a token is not,
  // because that was already the only option over http.
  const bootstrapBlocked = mode === "bootstrap" && !secure && !insecureAccepted;
  const credentialsComplete =
    mode === "bootstrap"
      ? bootstrapUser !== "" && bootstrapPassword !== "" && !bootstrapBlocked
      : tokenId !== "" && tokenSecret !== "";

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogTrigger asChild>{trigger ?? defaultTrigger}</DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Add Proxmox Cluster</DialogTitle>
          <DialogDescription>
            {summary
              ? "Nexara created its own credential on the cluster."
              : showFingerprint
                ? "Verify the TLS certificate fingerprint before connecting."
                : "Connect a Proxmox VE cluster. Nexara can create its own API token, or you can paste one you made yourself."}
          </DialogDescription>
        </DialogHeader>

        {summary ? (
          <div className="space-y-4">
            <div className="rounded-lg border border-emerald-500/50 bg-emerald-500/10 p-4 space-y-3">
              <div className="flex items-center gap-2 text-emerald-600 dark:text-emerald-500">
                <ShieldCheck className="h-5 w-5 shrink-0" />
                <span className="font-medium">Credential created</span>
              </div>
              <p className="text-sm text-muted-foreground">
                Nexara authenticates to this cluster as{" "}
                <code className="font-mono text-xs">{summary.token_id}</code>. The
                secret is stored encrypted and was never sent to this browser.
              </p>
              <ul className="space-y-1">
                {summary.steps.map((step) => (
                  <li
                    key={`${step.step}-${step.detail ?? ""}`}
                    className="flex items-start gap-2 text-sm"
                  >
                    <Check className="mt-0.5 h-4 w-4 shrink-0 text-emerald-600 dark:text-emerald-500" />
                    <span>
                      <span className="capitalize">{step.step}</span>{" "}
                      <span className="text-muted-foreground">
                        {step.status}
                        {step.detail != null && step.detail !== ""
                          ? ` — ${step.detail}`
                          : ""}
                      </span>
                    </span>
                  </li>
                ))}
              </ul>
              <p className="text-xs text-muted-foreground">
                Deleting this cluster can remove these again — Nexara offers it
                because it created them.
              </p>
            </div>

            {summaryConnectivity != null && !summaryConnectivity.reachable && (
              <p className="text-sm text-amber-600">
                Connectivity check failed: {summaryConnectivity.message}
              </p>
            )}

            <DialogFooter>
              <Button
                onClick={() => {
                  handleOpenChange(false);
                }}
              >
                Done
              </Button>
            </DialogFooter>
          </div>
        ) : !showFingerprint ? (
          <form onSubmit={handleFetchFingerprintSubmit} className="space-y-4">
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

            {privateWarning != null && (
              <PrivateAddressWarning
                ip={privateWarning.ip}
                url={apiUrl}
                onConfirm={handleConfirmPrivate}
                onCancel={() => { setPrivateWarning(null); }}
                pending={fetchingFingerprint}
              />
            )}

            {privateWarning == null && fingerprintError != null && (
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
              <div className="rounded-lg border border-amber-500/50 bg-amber-500/10 p-4 space-y-3">
                <div className="flex items-center gap-2 text-amber-600 dark:text-amber-500">
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
              <div className="rounded-lg border border-emerald-500/50 bg-emerald-500/10 p-4">
                <div className="flex items-center gap-2 text-emerald-600 dark:text-emerald-500">
                  <ShieldCheck className="h-5 w-5 shrink-0" />
                  <span className="font-medium">Trusted Certificate</span>
                </div>
                <p className="text-sm text-muted-foreground mt-1">
                  The server at <strong>{apiUrl}</strong> has a valid certificate signed by a trusted CA.
                </p>
              </div>
            )}

            <Tabs
              value={mode}
              onValueChange={(v) => {
                setMode(v as CredentialMode);
                clearSubmitError();
              }}
            >
              <TabsList className="grid w-full grid-cols-2">
                <TabsTrigger value="bootstrap">Create a token for me</TabsTrigger>
                <TabsTrigger value="token">I have a token</TabsTrigger>
              </TabsList>
            </Tabs>

            {mode === "bootstrap" ? (
              <div className="space-y-4">
                <p className="text-sm text-muted-foreground">
                  Nexara signs in once with a privileged account, creates a
                  dedicated <code className="font-mono text-xs">{`nexara@pve`}</code>{" "}
                  user with the Administrator role, and issues itself an API
                  token. The password is used for that one request and never
                  stored.
                </p>

                {!secure && (
                  <div className="rounded-lg border border-amber-500/50 bg-amber-500/10 p-4 space-y-3">
                    <div className="flex items-center gap-2 text-amber-600 dark:text-amber-500">
                      <ShieldAlert className="h-5 w-5 shrink-0" />
                      <span className="font-medium">This page is not on HTTPS</span>
                    </div>
                    <p className="text-sm text-muted-foreground">
                      A Proxmox password typed here would travel in the clear.
                      Serve Nexara over HTTPS, or paste an API token instead —
                      a token is scoped to this cluster and can be revoked on
                      its own.
                    </p>
                    <div className="flex items-center gap-2">
                      <Checkbox
                        id="accept-insecure"
                        checked={insecureAccepted}
                        onCheckedChange={(checked) => { setInsecureAccepted(Boolean(checked)); }}
                      />
                      <Label htmlFor="accept-insecure" className="text-sm">
                        I trust this network and want to continue anyway
                      </Label>
                    </div>
                  </div>
                )}

                <div className="space-y-2">
                  <Label htmlFor="bootstrap-user">Proxmox Username</Label>
                  <Input
                    id="bootstrap-user"
                    placeholder="root@pam"
                    autoComplete="off"
                    data-1p-ignore
                    data-lpignore="true"
                    value={bootstrapUser}
                    onChange={(e) => { setBootstrapUser(e.target.value); }}
                    disabled={bootstrapBlocked}
                  />
                </div>
                <div className="space-y-2">
                  <Label htmlFor="bootstrap-password">Proxmox Password</Label>
                  <Input
                    id="bootstrap-password"
                    type="password"
                    // "off" is widely ignored for password inputs;
                    // "new-password" is the value browsers actually honour.
                    autoComplete="new-password"
                    placeholder="Used once, never stored"
                    value={bootstrapPassword}
                    onChange={(e) => { setBootstrapPassword(e.target.value); }}
                    disabled={bootstrapBlocked}
                  />
                </div>

                {otpRequired && (
                  <div className="space-y-2">
                    <Label htmlFor="bootstrap-otp">One-Time Code</Label>
                    <Input
                      id="bootstrap-otp"
                      inputMode="numeric"
                      autoComplete="one-time-code"
                      placeholder="123456"
                      value={otp}
                      onChange={(e) => { setOtp(e.target.value); }}
                    />
                  </div>
                )}

                <div className="space-y-2">
                  <Label htmlFor="bootstrap-token-name">Token Name</Label>
                  <Input
                    id="bootstrap-token-name"
                    placeholder="nexara"
                    value={tokenName}
                    onChange={(e) => { setTokenName(e.target.value); }}
                    disabled={bootstrapBlocked}
                  />
                  {tokenConflict && (
                    <p className="text-xs text-muted-foreground">
                      That name is taken on this cluster. Pick another, or
                      delete the existing token in Proxmox — Nexara will not
                      quietly create a second one under a different name.
                    </p>
                  )}
                </div>
              </div>
            ) : (
              <div className="space-y-4">
                {/*
                  * autoComplete matters on this pair, and "off" alone is not
                  * enough. Chrome anchors a saved-login fill on an adjacent
                  * type="password" field and then fills the text input above it
                  * as the username — which put the operator's own email and
                  * saved password into these boxes, ready to be POSTed to the
                  * server as a Proxmox token. Marking the secret
                  * "new-password" breaks that pairing, which is what already
                  * keeps the bootstrap username field clean.
                  */}
                <div className="space-y-2">
                  <Label htmlFor="token-id">API Token ID</Label>
                  <Input
                    id="token-id"
                    placeholder="root@pam!nexara"
                    autoComplete="off"
                    data-1p-ignore
                    data-lpignore="true"
                    value={tokenId}
                    onChange={(e) => { setTokenId(e.target.value); }}
                  />
                </div>
                <div className="space-y-2">
                  <Label htmlFor="token-secret">API Token Secret</Label>
                  <Input
                    id="token-secret"
                    type="password"
                    placeholder="xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"
                    autoComplete="new-password"
                    data-1p-ignore
                    data-lpignore="true"
                    value={tokenSecret}
                    onChange={(e) => { setTokenSecret(e.target.value); }}
                  />
                </div>
              </div>
            )}

            {bootstrapError != null && (
              <p className="text-sm text-destructive">{bootstrapError}</p>
            )}

            {bootstrapError == null && createCluster.error != null && (
              <p className="text-sm text-destructive">
                {createCluster.error instanceof Error
                  ? createCluster.error.message
                  : "Failed to create cluster"}
              </p>
            )}

            {createCluster.data?.connectivity != null &&
              !createCluster.data.connectivity.reachable && (
                <p className="text-sm text-amber-600">
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
                disabled={
                  !fingerprintAccepted ||
                  !credentialsComplete ||
                  createCluster.isPending
                }
              >
                {createCluster.isPending ? (
                  "Adding..."
                ) : mode === "bootstrap" ? (
                  <>
                    <Wand2 className="mr-2 h-4 w-4" />
                    Create Token &amp; Add
                  </>
                ) : (
                  "Add Cluster"
                )}
              </Button>
            </DialogFooter>
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}
