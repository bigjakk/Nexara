import { ShieldAlert } from "lucide-react";
import { Button } from "@/components/ui/button";
import { WarningCallout } from "@/components/WarningCallout";

interface PrivateAddressWarningProps {
  ip: string;
  url?: string;
  onConfirm: () => void;
  onCancel?: () => void;
  pending?: boolean;
  /**
   * The gate behind it refuses loopback outright (notification channels, the
   * OIDC issuer), so only a private address can reach this prompt and it must
   * not offer a loopback one.
   */
  privateOnly?: boolean;
}

/**
 * What the gate found `ip` to be, as the server's classes go
 * (handlers/url_policy.go): a loopback or link-local address is said to be
 * one, and any other it confirms is on a private network. The shared text said
 * "private/loopback" of every one.
 */
function addressKind(ip: string): string {
  const v4 = /^(?:::ffff:)?(\d+)\.(\d+)\.\d+\.\d+$/i.exec(ip);
  if (v4 !== null) {
    if (v4[1] === "127") return "a loopback address";
    if (v4[1] === "169" && v4[2] === "254") return "a link-local address";
    return "an address on a private network";
  }
  const v6 = ip.toLowerCase();
  if (v6 === "::1") return "a loopback address";
  if (/^fe[89ab]/.test(v6)) return "a link-local address";
  return "an address on a private network";
}

/**
 * Inline confirm gate shown when the backend rejects an API URL for resolving
 * to a private (or, for the cluster-style gates, loopback) IP. The user can
 * opt in (homelab use case) or back out. Cloud metadata, multicast, and
 * unspecified addresses are hard-blocked upstream and never reach this
 * component; with `privateOnly`, loopback and link-local ones are too.
 */
export function PrivateAddressWarning({
  ip,
  url,
  onConfirm,
  onCancel,
  pending = false,
  privateOnly = false,
}: PrivateAddressWarningProps) {
  const kind = privateOnly
    ? "an address on a private network"
    : addressKind(ip);
  return (
    <WarningCallout title="Private network address" icon={ShieldAlert}>
      <p>
        {url != null && url !== "" ? (
          <>
            <strong className="break-all text-foreground">{url}</strong>{" "}
            resolves to {kind} (<code className="font-mono">{ip}</code>).
          </>
        ) : (
          <>
            That URL resolves to {kind} (<code className="font-mono">{ip}</code>
            ).
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
