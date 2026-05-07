import { ShieldAlert } from "lucide-react";
import { Button } from "@/components/ui/button";

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
    <div className="space-y-3 rounded-lg border border-yellow-500/50 bg-yellow-500/10 p-4">
      <div className="flex items-center gap-2 text-yellow-600 dark:text-yellow-500">
        <ShieldAlert className="h-5 w-5 shrink-0" />
        <span className="font-medium">Private network address</span>
      </div>
      <p className="text-sm text-muted-foreground">
        {url != null && url !== "" ? (
          <>
            <strong className="break-all">{url}</strong> resolves to a
            private/loopback IP (<code className="font-mono">{ip}</code>).
          </>
        ) : (
          <>
            That URL resolves to a private/loopback IP (
            <code className="font-mono">{ip}</code>).
          </>
        )}{" "}
        This is fine for self-hosted lab setups — confirm to continue, or go
        back and use a public address.
      </p>
      <div className="flex flex-wrap gap-2">
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
    </div>
  );
}
