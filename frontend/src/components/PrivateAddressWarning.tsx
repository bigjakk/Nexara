import { ShieldAlert } from "lucide-react";
import { Trans, useTranslation } from "react-i18next";
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
type AddressKind = "loopback" | "linkLocal" | "private";

function addressKind(ip: string): AddressKind {
  const v4 = /^(?:::ffff:)?(\d+)\.(\d+)\.\d+\.\d+$/i.exec(ip);
  if (v4 !== null) {
    if (v4[1] === "127") return "loopback";
    if (v4[1] === "169" && v4[2] === "254") return "linkLocal";
    return "private";
  }
  const v6 = ip.toLowerCase();
  if (v6 === "::1") return "loopback";
  if (/^fe[89ab]/.test(v6)) return "linkLocal";
  return "private";
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
  const { t } = useTranslation("clusters");
  const kind = privateOnly ? "private" : addressKind(ip);
  const hasUrl = url != null && url !== "";
  return (
    <WarningCallout title={t("privateAddress.title")} icon={ShieldAlert}>
      <p>
        <Trans
          t={t}
          i18nKey={
            hasUrl ? "privateAddress.urlResolves" : "privateAddress.resolves"
          }
          context={kind}
          values={{ url, ip }}
          components={{
            url: <strong className="break-all text-foreground" />,
            ip: <code className="font-mono" />,
          }}
        />
      </p>
      <div className="flex flex-wrap gap-2 pt-1.5">
        <Button
          type="button"
          size="sm"
          variant="outline"
          onClick={onConfirm}
          disabled={pending}
        >
          {pending
            ? t("privateAddress.confirming")
            : t("privateAddress.continue")}
        </Button>
        {onCancel != null && (
          <Button
            type="button"
            size="sm"
            variant="ghost"
            onClick={onCancel}
            disabled={pending}
          >
            {t("common:cancel")}
          </Button>
        )}
      </div>
    </WarningCallout>
  );
}
