import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Plus } from "lucide-react";
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
  DialogTrigger,
} from "@/components/ui/dialog";
import { PrivateAddressWarning } from "@/components/PrivateAddressWarning";
import { useCreateVeeamServer } from "../api/backup-queries";
import { useSaveOutcome } from "@/hooks/useSaveOutcome";
import { VeeamRequirementsNote } from "./VeeamRequirementsNote";
import { VeeamCertificateStep } from "./VeeamCertificateStep";
import { useVeeamFingerprint } from "./useVeeamFingerprint";

interface AddVeeamServerDialogProps {
  trigger?: React.ReactNode;
}

/**
 * Two-step add: fetch and confirm the TLS certificate, then connect.
 *
 * The connect step is where the server is actually contacted: the backend
 * negotiates an API revision, checks the build is 13.1 or newer, and reads the
 * licence before it stores anything. A failure there means nothing was saved.
 */
export function AddVeeamServerDialog({ trigger }: AddVeeamServerDialogProps) {
  const { t } = useTranslation("backup");
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [baseUrl, setBaseUrl] = useState("");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [allowPrivate, setAllowPrivate] = useState(false);

  const cert = useVeeamFingerprint();
  const createServer = useCreateVeeamServer();
  const settle = useSaveOutcome(open);

  function resetForm() {
    setName("");
    setBaseUrl("");
    setUsername("");
    setPassword("");
    setAllowPrivate(false);
    cert.reset();
    createServer.reset();
  }

  function handleOpenChange(nextOpen: boolean) {
    setOpen(nextOpen);
    if (!nextOpen) resetForm();
  }

  function handleFetchFingerprint(e: React.SyntheticEvent<HTMLFormElement>) {
    e.preventDefault();
    void cert.fetchFor(baseUrl, allowPrivate);
  }

  function handleConfirmPrivate() {
    setAllowPrivate(true);
    void cert.fetchFor(baseUrl, true);
  }

  function handleCreate() {
    settle(
      createServer.mutateAsync({
        name,
        base_url: baseUrl,
        username,
        password,
        tls_fingerprint: cert.pinToStore(),
        ...(allowPrivate ? { allow_private_address: true } : {}),
      }),
      {
        action: t("veeamServerDialog.addingAction", { name }),
        onSuccess: () => {
          setOpen(false);
          resetForm();
        },
        onError: () => undefined, // shown below from the mutation's state
      },
    );
  }

  const defaultTrigger = (
    <Button className="gap-1.5">
      <Plus className="h-4 w-4" aria-hidden="true" />
      {t("veeamServerDialog.addTitle")}
    </Button>
  );

  // Bound to a local so TypeScript narrows it inside the branch; a boolean
  // flag would leave it as FingerprintResponse | null. Keyed on the address
  // currently in the form, so an in-flight edit cannot leave a stale pin on
  // screen under a different host's name.
  const certificate = cert.fingerprintFor(baseUrl);

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogTrigger asChild>{trigger ?? defaultTrigger}</DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("veeamServerDialog.addTitle")}</DialogTitle>
          <DialogDescription>
            {certificate !== null
              ? t("serverDialog.verifyDescription")
              : t("veeamServerDialog.addDescription")}
          </DialogDescription>
        </DialogHeader>

        {certificate === null ? (
          <form onSubmit={handleFetchFingerprint} className="space-y-4">
            <VeeamRequirementsNote />

            <div className="space-y-2">
              <Label htmlFor="veeam-name">{t("serverDialog.serverName")}</Label>
              <Input
                id="veeam-name"
                placeholder={t("veeamServerDialog.serverNamePlaceholder")}
                value={name}
                onChange={(e) => {
                  setName(e.target.value);
                }}
                required
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="veeam-base-url">
                {t("veeamServerDialog.restApiUrl")}
              </Label>
              <Input
                id="veeam-base-url"
                placeholder="https://veeam.example.com:9419"
                value={baseUrl}
                onChange={(e) => {
                  setBaseUrl(e.target.value);
                }}
                required
              />
              <p className="text-xs text-muted-foreground">
                {t("veeamServerDialog.restApiPortHint")}
              </p>
            </div>
            <div className="space-y-2">
              <Label htmlFor="veeam-username">
                {t("veeamServerDialog.username")}
              </Label>
              <Input
                id="veeam-username"
                placeholder="DOMAIN\administrator"
                value={username}
                onChange={(e) => {
                  setUsername(e.target.value);
                }}
                required
              />
              <p className="text-xs text-muted-foreground">
                {t("veeamServerDialog.usernameHint")}
              </p>
            </div>
            <div className="space-y-2">
              <Label htmlFor="veeam-password">
                {t("veeamServerDialog.password")}
              </Label>
              <Input
                id="veeam-password"
                type="password"
                value={password}
                onChange={(e) => {
                  setPassword(e.target.value);
                }}
                required
              />
            </div>

            {cert.privateWarning != null && (
              <PrivateAddressWarning
                ip={cert.privateWarning.ip}
                url={baseUrl}
                onConfirm={handleConfirmPrivate}
                onCancel={cert.clearPrivateWarning}
                pending={cert.pending}
              />
            )}

            {cert.privateWarning == null && cert.error != null && (
              <p className="text-sm text-destructive">{cert.error}</p>
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
              <Button type="submit" disabled={cert.pending}>
                {cert.pending
                  ? t("common:connecting")
                  : t("veeamServerDialog.continue")}
              </Button>
            </DialogFooter>
          </form>
        ) : (
          <div className="space-y-4">
            <VeeamCertificateStep
              url={baseUrl}
              fingerprint={certificate}
              accepted={cert.accepted}
              onAcceptedChange={cert.setAccepted}
              idPrefix="add-veeam"
            />

            {createServer.error != null && (
              <p className="text-sm text-destructive">
                {createServer.error instanceof Error
                  ? createServer.error.message
                  : t("veeamServerDialog.addFailed")}
              </p>
            )}

            <DialogFooter>
              <Button type="button" variant="outline" onClick={cert.reset}>
                {t("common:back")}
              </Button>
              <Button
                onClick={handleCreate}
                disabled={!cert.accepted || createServer.isPending}
              >
                {createServer.isPending
                  ? t("common:connecting")
                  : t("serverDialog.addServer")}
              </Button>
            </DialogFooter>
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}
