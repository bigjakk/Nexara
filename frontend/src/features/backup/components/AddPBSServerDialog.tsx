import { useState } from "react";
import { Trans, useTranslation } from "react-i18next";
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
import { apiPath } from "@/lib/api-path";
import {
  privateAddressWarningFromError,
  type PrivateAddressWarning as PrivateAddressDetails,
} from "@/lib/private-address";
import { PrivateAddressWarning } from "@/components/PrivateAddressWarning";
import { useSaveOutcome } from "@/hooks/useSaveOutcome";
import { usePermissions } from "@/hooks/usePermissions";
import { useCanAddPBSServer } from "../hooks/useCanAddPBSServer";

interface FingerprintResponse {
  fingerprint: string;
  self_signed: boolean;
}

interface AddPBSServerDialogProps {
  trigger?: React.ReactNode;
}

export function AddPBSServerDialog({ trigger }: AddPBSServerDialogProps) {
  const { t } = useTranslation("backup");
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
  const [fingerprintError, setFingerprintError] = useState<string | null>(null);

  const [privateWarning, setPrivateWarning] =
    useState<PrivateAddressDetails | null>(null);
  const [allowPrivate, setAllowPrivate] = useState(false);

  const createPBS = useCreatePBSServer();
  const settle = useSaveOutcome(open);
  const clustersQuery = useClusters();
  const clusters = clustersQuery.data ?? [];
  const canAdd = useCanAddPBSServer();
  // A standalone server (no cluster) takes manage:pbs install-wide.
  const canStandalone = usePermissions().hasGlobalPermission("manage", "pbs");

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
    setPrivateWarning(null);
    setAllowPrivate(false);
    createPBS.reset();
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
        apiPath`/api/v1/clusters/fetch-fingerprint`,
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
      } else {
        setFingerprintError(
          err instanceof Error
            ? err.message
            : t("pbsServerDialog.fetchCertificateFailed"),
        );
      }
    } finally {
      setFetchingFingerprint(false);
    }
  }

  function handleFetchFingerprint(e: React.SyntheticEvent<HTMLFormElement>) {
    e.preventDefault();
    void fetchFingerprint(allowPrivate);
  }

  function handleConfirmPrivate() {
    setAllowPrivate(true);
    void fetchFingerprint(true);
  }

  function handleCreate() {
    settle(
      createPBS.mutateAsync({
        name,
        api_url: apiUrl,
        token_id: tokenId,
        token_secret: tokenSecret,
        tls_fingerprint: fingerprint?.fingerprint ?? "",
        cluster_id: clusterId || null,
        ...(allowPrivate ? { allow_private_address: true } : {}),
      }),
      {
        action: t("pbsServerDialog.addingAction", { name }),
        onSuccess: () => {
          setOpen(false);
          resetForm();
        },
        onError: () => undefined, // shown below from the mutation's state
      },
    );
  }

  function handleBack() {
    setFingerprint(null);
    setFingerprintAccepted(false);
    setFingerprintError(null);
  }

  const defaultTrigger = (
    <Button className="gap-1.5">
      <Plus className="h-4 w-4" aria-hidden="true" />
      {t("pbsServerDialog.addTitle")}
    </Button>
  );

  const showFingerprint = fingerprint !== null;

  if (!canAdd) return null;

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogTrigger asChild>{trigger ?? defaultTrigger}</DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("pbsServerDialog.addTitle")}</DialogTitle>
          <DialogDescription>
            {showFingerprint
              ? t("serverDialog.verifyDescription")
              : t("pbsServerDialog.addDescription")}
          </DialogDescription>
        </DialogHeader>

        {!showFingerprint ? (
          <form onSubmit={handleFetchFingerprint} className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="pbs-name">{t("serverDialog.serverName")}</Label>
              <Input
                id="pbs-name"
                placeholder={t("pbsServerDialog.serverNamePlaceholder")}
                value={name}
                onChange={(e) => {
                  setName(e.target.value);
                }}
                required
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="pbs-api-url">{t("pbsServerDialog.apiUrl")}</Label>
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
              <Label htmlFor="pbs-token-id">
                {t("pbsServerDialog.tokenId")}
              </Label>
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
              <Label htmlFor="pbs-token-secret">
                {t("pbsServerDialog.tokenSecret")}
              </Label>
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
              <Label htmlFor="pbs-cluster">
                {canStandalone
                  ? t("pbsServerDialog.associatedClusterOptional")
                  : t("pbsServerDialog.associatedCluster")}
              </Label>
              <select
                id="pbs-cluster"
                className="h-9 w-full rounded-md border border-input bg-transparent px-3 text-sm outline-hidden focus-visible:ring-2 focus-visible:ring-ring"
                value={clusterId}
                onChange={(e) => {
                  setClusterId(e.target.value);
                }}
                required={!canStandalone}
              >
                {canStandalone ? (
                  <option value="">{t("pbsServerDialog.none")}</option>
                ) : (
                  <option value="" disabled>
                    {t("selectCluster")}
                  </option>
                )}
                {clusters.map((c) => (
                  <option key={c.id} value={c.id}>
                    {c.name}
                  </option>
                ))}
              </select>
            </div>

            {privateWarning != null && (
              <PrivateAddressWarning
                ip={privateWarning.ip}
                url={apiUrl}
                onConfirm={handleConfirmPrivate}
                onCancel={() => {
                  setPrivateWarning(null);
                }}
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
                onClick={() => {
                  handleOpenChange(false);
                }}
              >
                {t("common:cancel")}
              </Button>
              <Button type="submit" disabled={fetchingFingerprint}>
                {fetchingFingerprint
                  ? t("common:connecting")
                  : t("common:connect")}
              </Button>
            </DialogFooter>
          </form>
        ) : (
          <div className="space-y-4">
            {fingerprint.self_signed ? (
              <div className="space-y-3 rounded-lg border border-amber-500/50 bg-amber-500/10 p-4">
                <div className="flex items-center gap-2 text-amber-700 dark:text-amber-400">
                  <ShieldAlert className="h-5 w-5 shrink-0" />
                  <span className="font-medium">
                    {t("serverDialog.selfSigned")}
                  </span>
                </div>
                <p className="text-sm text-muted-foreground">
                  <Trans
                    t={t}
                    i18nKey="pbsServerDialog.selfSignedDescription"
                    values={{ url: apiUrl }}
                    components={{ url: <strong className="break-all" /> }}
                  />
                </p>
                <div>
                  <p className="mb-1 text-xs text-muted-foreground">
                    {t("serverDialog.sha256Fingerprint")}
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
                    {t("serverDialog.acceptFingerprint")}
                  </Label>
                </div>
              </div>
            ) : (
              <div>
                <div className="flex items-center gap-2 text-sm font-medium">
                  <ShieldCheck className="h-4 w-4 shrink-0 text-emerald-600 dark:text-emerald-500" />
                  {t("serverDialog.trusted")}
                </div>
                <p className="mt-1 text-sm text-muted-foreground">
                  <Trans
                    t={t}
                    i18nKey="serverDialog.trustedDescription"
                    values={{ url: apiUrl }}
                    components={{ url: <strong className="break-all" /> }}
                  />
                </p>
              </div>
            )}

            {createPBS.error != null && (
              <p className="text-sm text-destructive">
                {createPBS.error instanceof Error
                  ? createPBS.error.message
                  : t("pbsServerDialog.addFailed")}
              </p>
            )}

            <DialogFooter>
              <Button type="button" variant="outline" onClick={handleBack}>
                {t("common:back")}
              </Button>
              <Button
                onClick={handleCreate}
                disabled={!fingerprintAccepted || createPBS.isPending}
              >
                {createPBS.isPending
                  ? t("pbsServerDialog.adding")
                  : t("serverDialog.addServer")}
              </Button>
            </DialogFooter>
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}
