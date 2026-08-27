import { ShieldAlert } from "lucide-react";
import { Button } from "@/components/ui/button";

interface ConfirmRequiredWarningProps {
  title: string;
  message: string;
  /** Label for the button that re-submits with the acknowledgement. */
  confirmLabel: string;
  onConfirm: () => void;
  onCancel: () => void;
  pending?: boolean;
}

/**
 * Inline confirm gate for a backend 422 the caller can override — the same
 * shape as PrivateAddressWarning, generalised so the LDAP transport, OIDC
 * cleartext-callback and cluster SSH-trust gates all read alike.
 *
 * The wording is the caller's, because each gate is refusing something
 * different and a generic "are you sure?" would tell the operator nothing
 * about what they are accepting.
 */
export function ConfirmRequiredWarning({
  title,
  message,
  confirmLabel,
  onConfirm,
  onCancel,
  pending = false,
}: ConfirmRequiredWarningProps) {
  return (
    <div
      data-testid="confirm-required-warning"
      className="space-y-3 rounded-lg border border-amber-500/50 bg-amber-500/10 p-4"
    >
      <div className="flex items-center gap-2 text-amber-600 dark:text-amber-500">
        <ShieldAlert className="h-5 w-5 shrink-0" />
        <span className="font-medium">{title}</span>
      </div>
      <p className="text-sm text-muted-foreground">{message}</p>
      <div className="flex flex-wrap gap-2">
        <Button
          type="button"
          size="sm"
          variant="outline"
          onClick={onConfirm}
          disabled={pending}
        >
          {pending ? "Confirming…" : confirmLabel}
        </Button>
        <Button type="button" size="sm" variant="ghost" onClick={onCancel}>
          Cancel
        </Button>
      </div>
    </div>
  );
}
