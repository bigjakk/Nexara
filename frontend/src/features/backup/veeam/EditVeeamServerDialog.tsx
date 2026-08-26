import { useEffect, useState } from "react";
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
    updateServer.mutate(
      {
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
      },
      {
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
          <DialogTitle>Edit Veeam Server</DialogTitle>
          <DialogDescription>
            {certificate !== null
              ? "The address changed. Verify the new host's certificate before saving."
              : "Changing the address or credentials re-tests the connection before saving."}
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
                  : "Failed to update the Veeam server"}
              </p>
            )}

            <DialogFooter>
              <Button type="button" variant="outline" onClick={cert.reset}>
                Back
              </Button>
              <Button
                onClick={() => {
                  submit(allowPrivate);
                }}
                disabled={!cert.accepted || updateServer.isPending}
              >
                {updateServer.isPending ? "Saving..." : "Save Changes"}
              </Button>
            </DialogFooter>
          </div>
        ) : (
          <form onSubmit={handleSubmit} className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="edit-veeam-name">Server Name</Label>
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
              <Label htmlFor="edit-veeam-base-url">REST API URL</Label>
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
                  Saving will ask you to confirm the new host&apos;s TLS
                  certificate, and needs the password again — the stored one is
                  only ever sent to the address it was saved for.
                </p>
              )}
            </div>
            <div className="space-y-2">
              <Label htmlFor="edit-veeam-username">Username</Label>
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
              <Label htmlFor="edit-veeam-password">Password</Label>
              <Input
                id="edit-veeam-password"
                type="password"
                placeholder={
                  addressChanged
                    ? "Required when the address changes"
                    : "Leave blank to keep the current password"
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
                Enabled
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
                  : "Failed to update the Veeam server"}
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
              <Button
                type="submit"
                disabled={updateServer.isPending || cert.pending}
              >
                {updateServer.isPending || cert.pending
                  ? "Saving..."
                  : addressChanged
                    ? "Continue"
                    : "Save Changes"}
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  );
}
