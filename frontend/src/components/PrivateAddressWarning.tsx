import { ShieldAlert } from "lucide-react";
import { Button } from "@/components/ui/button";
import { WarningCallout } from "@/components/WarningCallout";

interface PrivateAddressWarningProps {
  ip: string;
  url?: string;
  onConfirm: () => void;
  onCancel?: () => void;
  pending?: boolean;
}

/**
 * Inline confirm gate shown when the backend rejects an API URL for resolving
 * to a private/loopback IP. The user can opt in (homelab use case) or back
 * out. Cloud metadata, multicast, and unspecified addresses are hard-blocked
 * upstream and never reach this component.
 */
export function PrivateAddressWarning({
  ip,
  url,
  onConfirm,
  onCancel,
  pending = false,
}: PrivateAddressWarningProps) {
  return (
    <WarningCallout title="Private network address" icon={ShieldAlert}>
      <p>
        {url != null && url !== "" ? (
          <>
            <strong className="break-all text-foreground">{url}</strong>{" "}
            resolves to a private/loopback IP (
            <code className="font-mono">{ip}</code>).
          </>
        ) : (
          <>
            That URL resolves to a private/loopback IP (
            <code className="font-mono">{ip}</code>).
          </>
        )}{" "}
        This is fine for self-hosted lab setups. Confirm to continue, or go back
        and use a public address.
      </p>
      <div className="flex flex-wrap gap-2 pt-1.5">
        <Button
          type="button"
          size="sm"
          variant="outline"
          onClick={onConfirm}
          disabled={pending}
        >
          {pending ? "Confirming…" : "Continue with private address"}
        </Button>
        {onCancel != null && (
          <Button
            type="button"
            size="sm"
            variant="ghost"
            onClick={onCancel}
            disabled={pending}
          >
            Cancel
          </Button>
        )}
      </div>
    </WarningCallout>
  );
}
