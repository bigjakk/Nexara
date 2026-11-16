import { useEffect, useState } from "react";
import { AlertTriangle, Check, Copy } from "lucide-react";
import { Trans, useTranslation } from "react-i18next";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { copyText } from "@/lib/clipboard";

interface TokenSecretDialogProps {
  /** The full token id, e.g. "nexara@pve!api". Safe to display and log. */
  fullTokenId: string;
  /** The secret. Shown once; Proxmox will never return it again. */
  secret: string;
  /** Called when the operator dismisses the dialog. */
  onClose: () => void;
}

/**
 * Shows a freshly minted Proxmox API token secret exactly once.
 *
 * Proxmox has no read-back endpoint for token secrets: this dialog is the only
 * moment the value exists anywhere outside the cluster's own config. It is
 * deliberately modal and deliberately blunt about that, because closing it
 * without copying means the token is unrecoverable and has to be regenerated.
 *
 * The secret is held in local state and cleared on close. It is never written
 * to localStorage, never put in the URL, and never sent anywhere else.
 */
export function TokenSecretDialog({
  fullTokenId,
  secret,
  onClose,
}: TokenSecretDialogProps) {
  const { t } = useTranslation("access");
  const [copied, setCopied] = useState(false);
  const [copyFailed, setCopyFailed] = useState(false);

  useEffect(() => {
    if (!copied) return;
    const timer = setTimeout(() => {
      setCopied(false);
    }, 2000);
    return () => {
      clearTimeout(timer);
    };
  }, [copied]);

  const handleCopy = () => {
    setCopyFailed(false);
    void copyText(secret).then((ok) => {
      if (ok) setCopied(true);
      else setCopyFailed(true);
    });
  };

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("tokenSecret.title")}</DialogTitle>
          <DialogDescription>
            <Trans
              t={t}
              i18nKey="tokenSecret.ready"
              values={{ tokenId: fullTokenId }}
              components={{ code: <code className="font-mono text-xs" /> }}
            />
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-4 py-2">
          <div className="flex items-start gap-2.5 rounded-[8px] bg-amber-500/10 px-3.5 py-3">
            <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-amber-700 dark:text-amber-400" />
            <p className="min-w-0 text-sm">{t("tokenSecret.copyNow")}</p>
          </div>

          <div className="flex items-center gap-2">
            <code className="panel flex-1 rounded-[8px] px-3 py-2 font-mono text-sm break-all select-all">
              {secret}
            </code>
            <Button
              variant="outline"
              size="icon"
              className="shrink-0"
              onClick={handleCopy}
              aria-label={t("tokenSecret.copy")}
            >
              {copied ? (
                <Check className="h-4 w-4 text-emerald-600" />
              ) : (
                <Copy className="h-4 w-4" />
              )}
            </Button>
          </div>

          {copyFailed && (
            <p className="text-sm text-destructive">
              {t("tokenSecret.copyFailed")}
            </p>
          )}
        </div>

        <DialogFooter>
          <Button onClick={onClose}>{t("tokenSecret.done")}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
