import { ShieldAlert, ShieldCheck } from "lucide-react";
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
  if (!fingerprint.self_signed) {
    return (
      <div>
        <div className="flex items-center gap-2 text-sm font-medium">
          <ShieldCheck className="h-4 w-4 shrink-0 text-emerald-600 dark:text-emerald-500" />
          Trusted certificate
        </div>
        <p className="mt-1 text-sm text-muted-foreground">
          The server at <strong className="break-all">{url}</strong> has a valid
          certificate signed by a trusted CA.
        </p>
      </div>
    );
  }

  const checkboxId = `${idPrefix}-accept-fingerprint`;

  return (
    <div className="space-y-3 rounded-lg border border-amber-500/50 bg-amber-500/10 p-4">
      <div className="flex items-center gap-2 text-amber-700 dark:text-amber-400">
        <ShieldAlert className="h-5 w-5 shrink-0" />
        <span className="font-medium">Self-signed certificate</span>
      </div>
      <p className="text-sm text-muted-foreground">
        The server at <strong className="break-all">{url}</strong> uses a
        self-signed certificate, the Veeam default. Verify this fingerprint
        matches your VBR host before accepting.
      </p>
      <div>
        <p className="mb-1 text-xs text-muted-foreground">
          SHA-256 fingerprint
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
          I have verified this fingerprint and trust this certificate
        </Label>
      </div>
    </div>
  );
}
