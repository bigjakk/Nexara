import { useEffect, useRef, useState } from "react";
import { AlertTriangle, Check, Copy, Download } from "lucide-react";

import {
  AlertDialog,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { copyText } from "@/lib/clipboard";
import { pbsKeyFileFingerprint, pbsKeyFileName } from "../lib/pbs-encryption";

interface PBSKeySaveDialogProps {
  /** The Proxmox storage id the key belongs to. */
  storage: string;
  /**
   * The Nexara cluster the storage is on — a storage id is unique only within
   * one cluster. `name` is null when the cluster's name was not to hand.
   */
  cluster: { id: string; name: string | null };
  /**
   * The key file Proxmox generated, exactly as the response carried it — or
   * "" when the request asked for one and the response did not carry it.
   */
  keyText: string;
  /**
   * Set when another key was generated for the same storage after this one:
   * Proxmox then uses only the last. `fingerprint` is that last key's short
   * fingerprint, or null when its key file did not say.
   */
  replacedBy?: { fingerprint: string | null } | undefined;
  /** Called once the operator has confirmed the key is saved. */
  onDone: () => void;
}

/**
 * Shows a PBS client encryption key that Proxmox has just generated, once.
 *
 * Nexara keeps no copy and Proxmox keeps one only on the cluster, at
 * /etc/pve/priv/storage/<storage>.enc, so this is the operator's one chance to
 * take the copy that restores the storage's backups if the cluster is lost.
 * Like the Proxmox GUI's "Important: Save your Encryption Key" window
 * (PBSKeyShow in pve-manager www/manager6/storage/PBSEdit.js) it cannot be
 * dismissed by accident: it is an AlertDialog, which ignores a click outside
 * it and has no close button, Escape is swallowed, and Done stays disabled
 * until "I have saved this key" is ticked.
 *
 * Each key names the storage and cluster it is for, and the short fingerprint
 * Proxmox shows for it, the one the storage's Edit dialog shows for its
 * current key, so two keys generated for one storage can be told apart — and
 * a key that another replaced says so.
 *
 * This keeps nothing itself: the key lives in usePBSKeyStore until Done. It is
 * never written to storage, put in a URL or sent anywhere.
 */
export function PBSKeySaveDialog({
  storage,
  cluster,
  keyText,
  replacedBy,
  onDone,
}: PBSKeySaveDialogProps) {
  const [saved, setSaved] = useState(false);
  const [copied, setCopied] = useState(false);
  const [copyFailed, setCopyFailed] = useState(false);
  const keyField = useRef<HTMLTextAreaElement>(null);
  const copyButton = useRef<HTMLButtonElement>(null);
  const savedBox = useRef<HTMLButtonElement>(null);
  const delivered = keyText !== "";
  const fingerprint = pbsKeyFileFingerprint(keyText);
  const storageOnCluster = (
    <>
      <span className="font-mono">{storage}</span> on{" "}
      {cluster.name !== null ? (
        <>
          cluster <span className="font-medium">{cluster.name}</span>
        </>
      ) : (
        <>
          the cluster with ID <span className="font-mono">{cluster.id}</span>
        </>
      )}
    </>
  );

  useEffect(() => {
    if (!copied) return;
    const timer = setTimeout(() => {
      setCopied(false);
    }, 2000);
    return () => {
      clearTimeout(timer);
    };
  }, [copied]);

  function handleCopy() {
    setCopyFailed(false);
    // From the key field itself when the clipboard API is out of reach: this
    // dialog traps focus, which a temporary field outside it would lose.
    void copyText(keyText, keyField.current ?? undefined).then((ok) => {
      if (ok) setCopied(true);
      else setCopyFailed(true);
    });
  }

  function handleDownload() {
    const link = document.createElement("a");
    link.href = URL.createObjectURL(
      new Blob([keyText], { type: "application/json" }),
    );
    link.download = pbsKeyFileName(storage);
    link.click();
    URL.revokeObjectURL(link.href);
  }

  return (
    <AlertDialog
      open
      onOpenChange={(open) => {
        // Reached only by Escape once the box is ticked: an AlertDialog never
        // closes on an outside click, and Escape is swallowed until then.
        if (!open) onDone();
      }}
    >
      <AlertDialogContent
        onEscapeKeyDown={(e) => {
          if (!saved) e.preventDefault();
        }}
        onOpenAutoFocus={(e) => {
          // AlertDialogContent would focus its Cancel button, and this dialog
          // has none, so nothing would take focus — while the form closing
          // behind it hands focus back to its own trigger, under the overlay.
          // Focus Copy, or the checkbox when the key did not arrive: not the
          // key itself, which a screen reader would then read out in full.
          // Without scrolling, so the warning above stays in view.
          e.preventDefault();
          (copyButton.current ?? savedBox.current)?.focus({
            preventScroll: true,
          });
        }}
      >
        <AlertDialogHeader>
          <AlertDialogTitle>Save the encryption key</AlertDialogTitle>
          <AlertDialogDescription>
            {delivered ? (
              <>
                Proxmox generated a new client encryption key for{" "}
                {storageOnCluster}. This is the only time it is shown.
              </>
            ) : (
              <>
                Proxmox generated a new client encryption key for{" "}
                {storageOnCluster}, but did not send it back, so Nexara cannot
                show it.
              </>
            )}
          </AlertDialogDescription>
          {fingerprint && (
            <p className="text-sm">
              Fingerprint{" "}
              <code
                className="font-mono text-xs"
                title={fingerprint.fingerprint}
              >
                {fingerprint.shortFingerprint}
              </code>
            </p>
          )}
        </AlertDialogHeader>

        {replacedBy && (
          <div className="space-y-1 rounded-md border border-destructive/40 bg-destructive/5 p-3 text-sm">
            <p>
              <strong>Not the current key.</strong> Another key was generated
              for {storageOnCluster} after this one, and only the last one
              generated is current
              {replacedBy.fingerprint ? (
                <>
                  {" "}
                  — fingerprint{" "}
                  <code className="font-mono text-xs">
                    {replacedBy.fingerprint}
                  </code>
                </>
              ) : null}
              . It is shown after this one.
            </p>
            <p>
              Save this one too if a backup may have been made with it: Proxmox
              keeps it, if at all, only as{" "}
              <code className="font-mono text-xs break-all">
                /etc/pve/priv/storage/{storage}.enc.old
              </code>
              , until another key is generated.
            </p>
          </div>
        )}

        <div className="flex items-start gap-2 rounded-md border border-amber-200 bg-amber-50 p-3 dark:border-amber-800 dark:bg-amber-950/20">
          <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-amber-600 dark:text-amber-400" />
          <p className="text-sm text-amber-800 dark:text-amber-200">
            {replacedBy
              ? "Nexara does not keep a copy of this key."
              : "Nexara does not keep a copy of this key, and Proxmox keeps it only on this cluster."}{" "}
            Backups encrypted with it cannot be restored without it, so if the
            cluster is lost and you have no copy, those backups are lost too.
            Keep a copy somewhere off the cluster, such as a password manager.
          </p>
        </div>

        {delivered ? (
          <div className="space-y-2">
            <Textarea
              ref={keyField}
              aria-label="Encryption key"
              readOnly
              value={keyText}
              rows={6}
              spellCheck={false}
              className="font-mono text-xs break-all"
              onFocus={(e) => {
                e.currentTarget.select();
              }}
            />
            <div className="flex gap-2">
              <Button
                ref={copyButton}
                type="button"
                variant="outline"
                onClick={handleCopy}
              >
                {copied ? (
                  <Check className="mr-1 h-4 w-4 text-emerald-600" />
                ) : (
                  <Copy className="mr-1 h-4 w-4" />
                )}
                Copy
              </Button>
              <Button type="button" variant="outline" onClick={handleDownload}>
                <Download className="mr-1 h-4 w-4" />
                Download
              </Button>
            </div>
            {copyFailed && (
              <p className="text-sm text-destructive">
                Copy failed. Select the key above and copy it manually (Ctrl+C).
              </p>
            )}
          </div>
        ) : (
          <p className="text-sm">
            Copy it from any node of the cluster now:{" "}
            <code className="font-mono text-xs break-all">
              /etc/pve/priv/storage/{storage}.enc
            </code>
          </p>
        )}

        <div className="flex items-center gap-2">
          <Checkbox
            ref={savedBox}
            id="pbs-key-saved"
            checked={saved}
            onCheckedChange={(checked) => {
              setSaved(checked === true);
            }}
          />
          <Label htmlFor="pbs-key-saved" className="text-sm font-normal">
            I have saved this key
          </Label>
        </div>

        <AlertDialogFooter>
          <Button disabled={!saved} onClick={onDone}>
            Done
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
