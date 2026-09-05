import { useState } from "react";
import { AlertTriangle } from "lucide-react";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { CopyableName } from "@/components/CopyableName";

interface SelfCredentialConfirmProps {
  /** The server's explanation of what is about to break. */
  message: string;
  /** The identifier the operator must type to confirm. */
  confirmValue: string;
  /** Verb for the confirm button, e.g. "Revoke Token". */
  actionLabel: string;
  pending: boolean;
  onCancel: () => void;
  onConfirm: () => void;
}

/**
 * Confirms an action that would destroy the credential Nexara uses to reach
 * the cluster.
 *
 * The server refuses these by default and returns 409 with an explanation;
 * this is the deliberate override. It is type-the-name rather than a plain
 * confirm because the consequence is not recoverable from inside Nexara — once
 * the token is gone, the cluster goes unreachable until someone reconfigures
 * it with fresh credentials, which cannot be done through the very screen that
 * just broke.
 *
 * It stays an override rather than a hard block: rotating credentials by hand
 * is legitimate, and refusing outright would push the operator to do it in the
 * Proxmox UI where Nexara cannot warn them at all.
 */
export function SelfCredentialConfirm({
  message,
  confirmValue,
  actionLabel,
  pending,
  onCancel,
  onConfirm,
}: SelfCredentialConfirmProps) {
  const [typed, setTyped] = useState("");

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) onCancel();
      }}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle>This will cut off Nexara&apos;s access</DialogTitle>
          <DialogDescription>{message}</DialogDescription>
        </DialogHeader>

        <div className="space-y-4 py-2">
          <div className="flex items-start gap-2 rounded-md border border-amber-200 bg-amber-50 p-3 dark:border-amber-800 dark:bg-amber-950/20">
            <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-amber-600 dark:text-amber-400" />
            <p className="text-sm text-amber-800 dark:text-amber-200">
              After this, the cluster will show as unreachable until you update
              its credentials in Nexara. You cannot do that from this screen.
            </p>
          </div>

          <div>
            <p className="mb-2 text-sm">
              Type <CopyableName name={confirmValue} /> to confirm.
            </p>
            <Input
              value={typed}
              onChange={(e) => {
                setTyped(e.target.value);
              }}
              placeholder={confirmValue}
              aria-label="Confirmation text"
            />
          </div>
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={onCancel}>
            Cancel
          </Button>
          <Button
            variant="destructive"
            disabled={typed !== confirmValue || pending}
            onClick={onConfirm}
          >
            {pending ? "Working..." : actionLabel}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
