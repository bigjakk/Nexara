import { useEffect, useState } from "react";
import { AlertTriangle, Check, Copy } from "lucide-react";

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
          <DialogTitle>API Token Created</DialogTitle>
          <DialogDescription>
            <code className="font-mono text-xs">{fullTokenId}</code> is ready to
            use.
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-4 py-2">
          <div className="flex items-start gap-2 rounded-md border border-amber-200 bg-amber-50 p-3 dark:border-amber-800 dark:bg-amber-950/20">
            <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-amber-600 dark:text-amber-400" />
            <p className="text-sm text-amber-800 dark:text-amber-200">
              Copy this secret now. Proxmox shows it only once — there is no way
              to retrieve it later, and recovering from a lost secret means
              regenerating the token.
            </p>
          </div>

          <div className="flex items-center gap-2">
            <code className="flex-1 rounded-md bg-muted px-3 py-2 font-mono text-sm break-all select-all">
              {secret}
            </code>
            <Button
              variant="outline"
              size="icon"
              className="shrink-0"
              onClick={handleCopy}
              aria-label="Copy token secret"
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
              Copy failed. Select the secret above and copy it manually
              (Ctrl+C).
            </p>
          )}
        </div>

        <DialogFooter>
          <Button onClick={onClose}>Done</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
