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

import { holdFocusInDialog } from "./holdFocusInDialog";

interface SelfCredentialConfirmProps {
  /**
   * The action being confirmed, and what it does — "Revoking this token will
   * cut off Nexara's access". This dialog is raised for several different
   * actions, and the title is how the operator tells them apart before reading
   * on.
   */
  title: string;
  /** The server's explanation of what is about to break. */
  message: string;
  /** The identifier the operator must type to confirm. */
  confirmValue: string;
  /** Verb for the confirm button, e.g. "Revoke Token". */
  actionLabel: string;
  /** The confirmed request is in flight; see the note on dismissing below. */
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
 *
 * The hold starts when the pending state reaches the render, a macrotask after
 * the click. From then until the request settles the dialog cannot be
 * dismissed: Cancel is disabled, and Escape, an outside click and the close
 * button are ignored. Closing would not recall the request, so "Cancel" would
 * leave the credential to go anyway, with the operator believing they had
 * stopped it. Focus is moved to the dialog as the request goes out, because the
 * button that had it is disabled by then (see holdFocusInDialog).
 */
export function SelfCredentialConfirm({
  title,
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
      // Escape, an outside click and the close button all end here, so this
      // one check holds them. The footer's Cancel calls onCancel itself, and is
      // held by being disabled.
      onOpenChange={(open) => {
        if (!open && !pending) onCancel();
      }}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
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
          <Button variant="outline" disabled={pending} onClick={onCancel}>
            Cancel
          </Button>
          <Button
            variant="destructive"
            disabled={typed !== confirmValue || pending}
            onClick={(e) => {
              onConfirm();
              holdFocusInDialog(e);
            }}
          >
            {pending ? "Working..." : actionLabel}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
