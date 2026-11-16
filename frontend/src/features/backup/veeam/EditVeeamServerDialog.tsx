import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
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
  privateAddressWarningFromError,
  type PrivateAddressWarning as PrivateAddressDetails,
} from "@/lib/private-address";
import { PrivateAddressWarning } from "@/components/PrivateAddressWarning";
import { useUpdateVeeamServer } from "../api/backup-queries";
import { useSaveOutcome } from "@/hooks/useSaveOutcome";
import type { VeeamServer } from "../types/backup";
import { VeeamCertificateStep } from "./VeeamCertificateStep";
import { useVeeamFingerprint } from "./useVeeamFingerprint";

interface EditVeeamServerDialogProps {
  server: VeeamServer;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

/**
 * Edits a registered server.
 *
 * The password field starts empty and is only submitted when the operator
 * types one — the stored credential is never sent to the browser, so there is
 * nothing to pre-fill and an empty field means "leave it alone".
 *
 * Changing the address inserts a certificate step, because a pinned
 * fingerprint belongs to one host. Carrying the old pin to a new address makes
 * the re-test fail on a mismatch every time, with no way to fix it short of
 * deleting the server and re-entering the password. Confirming the new host's
 * certificate re-pins it — or clears the pin outright when the new address
 * presents a CA-signed chain.
 *
 * Renaming or disabling touches neither, so an unreachable server can still be
 * disabled.
 */
export function EditVeeamServerDialog({
  server,
  open,
  onOpenChange,
}: EditVeeamServerDialogProps) {
  const { t } = useTranslation("backup");
  const [name, setName] = useState(server.name);
  const [baseUrl, setBaseUrl] = useState(server.base_url);
  const [username, setUsername] = useState(server.username);
  const [password, setPassword] = useState("");
  const [enabled, setEnabled] = useState(server.enabled);
  const [allowPrivate, setAllowPrivate] = useState(false);

  // Only set for errors raised by the update itself; the certificate step has
  // its own via useVeeamFingerprint.
  const [updatePrivateWarning, setUpdatePrivateWarning] =
    useState<PrivateAddressDetails | null>(null);

  const cert = useVeeamFingerprint();
  const updateServer = useUpdateVeeamServer();
  // The dialog shows a failure itself (the hook opts out of the app's toast),
  // and one that lands after it was closed is toasted instead.
  const settle = useSaveOutcome(open ? server.id : null);

  const addressChanged = baseUrl !== server.base_url;
  // Bound to a local so TypeScript narrows it inside the branch below, and
  // keyed on the address currently in the form so a pin fetched for a
  // previous one is never shown or submitted.
  const certificate = addressChanged ? cert.fingerprintFor(baseUrl) : null;

  // Re-seed when the dialog is opened, or when it is pointed at a different
  // server, so a stale draft never overwrites the wrong row.
  useEffect(() => {
    if (open) {
      setName(server.name);
      setBaseUrl(server.base_url);
      setUsername(server.username);
      setPassword("");
      setEnabled(server.enabled);
      setAllowPrivate(false);
      setUpdatePrivateWarning(null);
      cert.reset();
      updateServer.reset();
    }
    // cert.reset and updateServer are stable handles; including them would
    // re-seed the form on every render and discard what the operator typed.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, server]);

  function submit(allowPrivateNow: boolean) {
    setUpdatePrivateWarning(null);
    settle(
      updateServer.mutateAsync({
        id: server.id,
        name,
        base_url: baseUrl,
        username,
        enabled,
        ...(password !== "" ? { password } : {}),
        // Only send a pin when the address moved — otherwise the stored one
        // stays untouched, and sending "" would silently unpin the server.
        ...(addressChanged ? { tls_fingerprint: cert.pinToStore() } : {}),
        ...(allowPrivateNow ? { allow_private_address: true } : {}),
      }),
      {
        action: t("veeamServerDialog.savingAction", { name: server.name }),
        onSuccess: () => {
          onOpenChange(false);
        },
        onError: (err) => {
          const warn = privateAddressWarningFromError(err);
          if (warn != null) setUpdatePrivateWarning(warn);
        },
      },
    );
  }

  function handleSubmit(e: React.SyntheticEvent<HTMLFormElement>) {
    e.preventDefault();
    if (addressChanged) {
      void cert.fetchFor(baseUrl, allowPrivate);
      return;
    }
    submit(false);
  }

  function handleConfirmPrivateForCertificate() {
    setAllowPrivate(true);
    void cert.fetchFor(baseUrl, true);
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("veeamServerDialog.editTitle")}</DialogTitle>
          <DialogDescription>
            {certificate !== null
              ? t("veeamServerDialog.editVerifyDescription")
              : t("veeamServerDialog.editDescription")}
          </DialogDescription>
        </DialogHeader>

        {certificate !== null ? (
          <div className="space-y-4">
            <VeeamCertificateStep
              url={baseUrl}
              fingerprint={certificate}
              accepted={cert.accepted}
              onAcceptedChange={cert.setAccepted}
              idPrefix="edit-veeam"
            />

            {updateServer.error != null && (
              <p className="text-sm text-destructive">
                {updateServer.error instanceof Error
                  ? updateServer.error.message
                  : t("veeamServerDialog.updateFailed")}
              </p>
            )}

            <DialogFooter>
              <Button type="button" variant="outline" onClick={cert.reset}>
                {t("common:back")}
              </Button>
              <Button
                onClick={() => {
                  submit(allowPrivate);
                }}
                disabled={!cert.accepted || updateServer.isPending}
              >
                {updateServer.isPending
                  ? t("common:saving")
                  : t("common:saveChanges")}
              </Button>
            </DialogFooter>
          </div>
        ) : (
          <form onSubmit={handleSubmit} className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="edit-veeam-name">
                {t("serverDialog.serverName")}
              </Label>
              <Input
                id="edit-veeam-name"
                value={name}
                onChange={(e) => {
                  setName(e.target.value);
                }}
                required
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="edit-veeam-base-url">
                {t("veeamServerDialog.restApiUrl")}
              </Label>
              <Input
                id="edit-veeam-base-url"
                value={baseUrl}
                onChange={(e) => {
                  setBaseUrl(e.target.value);
                }}
                required
              />
              {addressChanged && (
                <p className="text-xs text-muted-foreground">
                  {t("veeamServerDialog.addressMoved")}
                </p>
              )}
            </div>
            <div className="space-y-2">
              <Label htmlFor="edit-veeam-username">
                {t("veeamServerDialog.username")}
              </Label>
              <Input
                id="edit-veeam-username"
                value={username}
                onChange={(e) => {
                  setUsername(e.target.value);
                }}
                required
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="edit-veeam-password">
                {t("veeamServerDialog.password")}
              </Label>
              <Input
                id="edit-veeam-password"
                type="password"
                placeholder={
                  addressChanged
                    ? t("veeamServerDialog.passwordRequired")
                    : t("veeamServerDialog.passwordKeep")
                }
                value={password}
                onChange={(e) => {
                  setPassword(e.target.value);
                }}
                required={addressChanged}
              />
            </div>
            <div className="flex items-center gap-2">
              <Checkbox
                id="edit-veeam-enabled"
                checked={enabled}
                onCheckedChange={(checked) => {
                  setEnabled(Boolean(checked));
                }}
              />
              <Label htmlFor="edit-veeam-enabled" className="text-sm">
                {t("common:enabled")}
              </Label>
            </div>

            {cert.privateWarning != null && (
              <PrivateAddressWarning
                ip={cert.privateWarning.ip}
                url={baseUrl}
                onConfirm={handleConfirmPrivateForCertificate}
                onCancel={cert.clearPrivateWarning}
                pending={cert.pending}
              />
            )}

            {updatePrivateWarning != null && (
              <PrivateAddressWarning
                ip={updatePrivateWarning.ip}
                url={baseUrl}
                onConfirm={() => {
                  setAllowPrivate(true);
                  submit(true);
                }}
                onCancel={() => {
                  setUpdatePrivateWarning(null);
                }}
                pending={updateServer.isPending}
              />
            )}

            {cert.error != null && (
              <p className="text-sm text-destructive">{cert.error}</p>
            )}

            {updatePrivateWarning == null && updateServer.error != null && (
              <p className="text-sm text-destructive">
                {updateServer.error instanceof Error
                  ? updateServer.error.message
                  : t("veeamServerDialog.updateFailed")}
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
                {t("common:cancel")}
              </Button>
              <Button
                type="submit"
                disabled={updateServer.isPending || cert.pending}
              >
                {updateServer.isPending || cert.pending
                  ? t("common:saving")
                  : addressChanged
                    ? t("veeamServerDialog.continue")
                    : t("common:saveChanges")}
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  );
}
