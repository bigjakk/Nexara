import { useState, useEffect } from "react";
import { Trans, useTranslation } from "react-i18next";
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
import { useSaveOutcome } from "@/hooks/useSaveOutcome";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import {
  privateAddressWarningFromError,
  type PrivateAddressWarning as PrivateAddressDetails,
} from "@/lib/private-address";
import { PrivateAddressWarning } from "@/components/PrivateAddressWarning";
import type { PBSServer } from "../types/backup";

interface EditPBSServerDialogProps {
  server: PBSServer;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /**
   * For a role that may not change the server (manage:pbs on its scope): the
   * settings are shown with every field disabled and no Save.
   */
  readOnly: boolean;
}

export function EditPBSServerDialog({
  server,
  open,
  onOpenChange,
  readOnly,
}: EditPBSServerDialogProps) {
  const { t } = useTranslation("backup");
  const [name, setName] = useState(server.name);
  const [apiUrl, setApiUrl] = useState(server.api_url);
  const [tokenId, setTokenId] = useState(server.token_id);
  const [tokenSecret, setTokenSecret] = useState("");
  const [tlsFingerprint, setTlsFingerprint] = useState(server.tls_fingerprint);
  const [clusterId, setClusterId] = useState(server.cluster_id ?? "");

  const [privateWarning, setPrivateWarning] =
    useState<PrivateAddressDetails | null>(null);
  const [allowPrivate, setAllowPrivate] = useState(false);

  const updatePBS = useUpdatePBSServer();
  // The dialog shows a failure itself (the hook opts out of the app's toast),
  // and one that lands after it was closed is toasted instead.
  const settle = useSaveOutcome(open ? server.id : null);
  const clustersQuery = useClusters();
  const clusters = clustersQuery.data ?? [];

  // The stored token secret is only ever sent to the address it was saved for,
  // so moving the address means re-entering it. The backend refuses the
  // combination outright (PBSHandler.Update); this makes the field required
  // before the request is worth sending, and says why.
  const addressChanged = apiUrl !== server.api_url;

  useEffect(() => {
    if (open) {
      setName(server.name);
      setApiUrl(server.api_url);
      setTokenId(server.token_id);
      setTokenSecret("");
      setTlsFingerprint(server.tls_fingerprint);
      setClusterId(server.cluster_id ?? "");
      setPrivateWarning(null);
      setAllowPrivate(false);
      updatePBS.reset();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, server]);

  function submit(allow: boolean) {
    const body: {
      id: string;
      name?: string;
      api_url?: string;
      token_id?: string;
      token_secret?: string;
      tls_fingerprint?: string;
      cluster_id?: string;
      allow_private_address?: boolean;
    } = { id: server.id };
    if (name !== server.name) body.name = name;
    if (apiUrl !== server.api_url) body.api_url = apiUrl;
    if (tokenId !== server.token_id) body.token_id = tokenId;
    if (tokenSecret.length > 0) body.token_secret = tokenSecret;
    if (tlsFingerprint !== server.tls_fingerprint)
      body.tls_fingerprint = tlsFingerprint;
    if ((clusterId || null) !== (server.cluster_id || null))
      body.cluster_id = clusterId || "";
    if (allow) body.allow_private_address = true;

    setPrivateWarning(null);

    settle(updatePBS.mutateAsync(body), {
      action: t("pbsServerDialog.savingAction", { name: server.name }),
      onSuccess: () => {
        onOpenChange(false);
      },
      onError: (err) => {
        const warn = privateAddressWarningFromError(err);
        if (warn != null) {
          setPrivateWarning(warn);
        }
      },
    });
  }

  function handleSubmit(e: React.SyntheticEvent) {
    e.preventDefault();
    submit(allowPrivate);
  }

  function handleConfirmPrivate() {
    setAllowPrivate(true);
    submit(true);
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            {readOnly
              ? t("pbsServerDialog.viewTitle")
              : t("pbsServerDialog.editTitle")}
          </DialogTitle>
          <DialogDescription>
            <Trans
              t={t}
              i18nKey={
                readOnly
                  ? "pbsServerDialog.viewDescription"
                  : "pbsServerDialog.editDescription"
              }
              values={{ name: server.name }}
              components={{ name: <strong /> }}
            />
          </DialogDescription>
        </DialogHeader>

        <form onSubmit={handleSubmit} className="space-y-4">
          {/* Disables every field at once; the buttons sit outside it. */}
          <fieldset disabled={readOnly} className="min-w-0 space-y-4">
            <div className="space-y-2">
              <Label htmlFor="edit-pbs-name">
                {t("serverDialog.serverName")}
              </Label>
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
              <Label htmlFor="edit-pbs-api-url">
                {t("pbsServerDialog.apiUrl")}
              </Label>
              <Input
                id="edit-pbs-api-url"
                value={apiUrl}
                onChange={(e) => {
                  setApiUrl(e.target.value);
                }}
                required
              />
              {addressChanged && (
                <p className="text-xs text-muted-foreground">
                  {t("pbsServerDialog.addressMoved")}
                </p>
              )}
            </div>
            <div className="space-y-2">
              <Label htmlFor="edit-pbs-token-id">
                {t("pbsServerDialog.tokenId")}
              </Label>
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
                {t("pbsServerDialog.tokenSecret")}{" "}
                {/* How to change it, which is not this role's to do. */}
                {!readOnly && (
                  <span className="text-muted-foreground font-normal">
                    {addressChanged
                      ? t("pbsServerDialog.tokenSecretRequired")
                      : t("pbsServerDialog.tokenSecretKeep")}
                  </span>
                )}
              </Label>
              <Input
                id="edit-pbs-token-secret"
                type="password"
                placeholder={
                  readOnly
                    ? undefined
                    : addressChanged
                      ? t("pbsServerDialog.tokenSecretReenter")
                      : t("pbsServerDialog.tokenSecretKeepPlaceholder")
                }
                value={tokenSecret}
                onChange={(e) => {
                  setTokenSecret(e.target.value);
                }}
                required={addressChanged}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="edit-pbs-fingerprint">
                {t("pbsServerDialog.tlsFingerprint")}
              </Label>
              <Input
                id="edit-pbs-fingerprint"
                value={tlsFingerprint}
                onChange={(e) => {
                  setTlsFingerprint(e.target.value);
                }}
                placeholder={t("serverDialog.sha256Fingerprint")}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="edit-pbs-cluster">
                {t("pbsServerDialog.associatedCluster")}
                <span className="ml-1 text-xs font-normal text-muted-foreground">
                  {t("pbsServerDialog.associatedClusterHint")}
                </span>
              </Label>
              <select
                id="edit-pbs-cluster"
                className="h-9 w-full rounded-md border border-input bg-transparent px-3 text-sm outline-hidden focus-visible:ring-2 focus-visible:ring-ring"
                value={clusterId}
                onChange={(e) => {
                  setClusterId(e.target.value);
                }}
              >
                <option value="">{t("pbsServerDialog.none")}</option>
                {clusters.map((c) => (
                  <option key={c.id} value={c.id}>
                    {c.name}
                  </option>
                ))}
              </select>
            </div>
          </fieldset>

          {privateWarning != null && (
            <PrivateAddressWarning
              ip={privateWarning.ip}
              url={apiUrl}
              onConfirm={handleConfirmPrivate}
              onCancel={() => {
                setPrivateWarning(null);
              }}
              pending={updatePBS.isPending}
            />
          )}

          {privateWarning == null && updatePBS.isError && (
            <p className="text-sm text-destructive">
              {updatePBS.error instanceof Error
                ? updatePBS.error.message
                : t("pbsServerDialog.updateFailed")}
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
              {readOnly ? t("common:close") : t("common:cancel")}
            </Button>
            {!readOnly && (
              <Button type="submit" disabled={updatePBS.isPending}>
                {updatePBS.isPending
                  ? t("common:saving")
                  : t("common:saveChanges")}
              </Button>
            )}
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
