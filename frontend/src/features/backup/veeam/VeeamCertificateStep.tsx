import { ShieldAlert, ShieldCheck } from "lucide-react";
import { Trans, useTranslation } from "react-i18next";
import { Checkbox } from "@/components/ui/checkbox";
import { Label } from "@/components/ui/label";
import type { FingerprintResponse } from "./useVeeamFingerprint";

interface VeeamCertificateStepProps {
  url: string;
  fingerprint: FingerprintResponse;
  accepted: boolean;
  onAcceptedChange: (accepted: boolean) => void;
  /** Distinguishes the checkbox from another instance on the same page. */
  idPrefix: string;
}

/**
 * Shows the certificate a host presented, and — for a self-signed one —
 * requires the operator to say they have checked it.
 *
 * VBR ships self-signed by default, so pinning is the usual path. But a
 * reverse-proxied deployment presents a publicly-valid chain, and reporting
 * which of the two it is beats assuming either.
 */
export function VeeamCertificateStep({
  url,
  fingerprint,
  accepted,
  onAcceptedChange,
  idPrefix,
}: VeeamCertificateStepProps) {
  const { t } = useTranslation("backup");
  if (!fingerprint.self_signed) {
    return (
      <div>
        <div className="flex items-center gap-2 text-sm font-medium">
          <ShieldCheck className="h-4 w-4 shrink-0 text-emerald-600 dark:text-emerald-500" />
          {t("serverDialog.trusted")}
        </div>
        <p className="mt-1 text-sm text-muted-foreground">
          <Trans
            t={t}
            i18nKey="serverDialog.trustedDescription"
            values={{ url }}
            components={{ url: <strong className="break-all" /> }}
          />
        </p>
      </div>
    );
  }

  const checkboxId = `${idPrefix}-accept-fingerprint`;

  return (
    <div className="space-y-3 rounded-lg border border-amber-500/50 bg-amber-500/10 p-4">
      <div className="flex items-center gap-2 text-amber-700 dark:text-amber-400">
        <ShieldAlert className="h-5 w-5 shrink-0" />
        <span className="font-medium">{t("serverDialog.selfSigned")}</span>
      </div>
      <p className="text-sm text-muted-foreground">
        <Trans
          t={t}
          i18nKey="veeamCertificate.selfSignedDescription"
          values={{ url }}
          components={{ url: <strong className="break-all" /> }}
        />
      </p>
      <div>
        <p className="mb-1 text-xs text-muted-foreground">
          {t("serverDialog.sha256Fingerprint")}
        </p>
        <code className="select-all break-all font-mono text-xs">
          {fingerprint.fingerprint}
        </code>
      </div>
      <div className="flex items-center gap-2">
        <Checkbox
          id={checkboxId}
          checked={accepted}
          onCheckedChange={(checked) => {
            onAcceptedChange(Boolean(checked));
          }}
        />
        <Label htmlFor={checkboxId} className="text-sm">
          {t("serverDialog.acceptFingerprint")}
        </Label>
      </div>
    </div>
  );
}
