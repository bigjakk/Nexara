import { ShieldAlert } from "lucide-react";
import { Button } from "@/components/ui/button";
import { WarningCallout } from "@/components/WarningCallout";

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
    <WarningCallout
      data-testid="confirm-required-warning"
      title={title}
      icon={ShieldAlert}
    >
      <p>{message}</p>
      <div className="flex flex-wrap gap-2 pt-1.5">
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
    </WarningCallout>
  );
}
