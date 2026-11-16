import { Info } from "lucide-react";
import { Trans, useTranslation } from "react-i18next";

/**
 * States the two hard requirements up front rather than letting an operator
 * discover them as an error after typing a password.
 *
 * Both are real refusals, not preferences: 13.0.x reports Proxmox jobs with no
 * type and no last-run time, so every number Nexara derived from them would be
 * silently wrong; and Proxmox VE backup is an Enterprise Plus capability that
 * Veeam itself enforces.
 */
export function VeeamRequirementsNote() {
  const { t } = useTranslation("backup");
  return (
    <div className="panel flex gap-2.5 rounded-[10px] px-[18px] py-3 text-[12.5px] text-muted-foreground">
      <Info className="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
      <div className="space-y-1">
        <p>
          <Trans
            t={t}
            i18nKey="veeamRequirements.versionAndLicence"
            components={{ strong: <strong /> }}
          />
        </p>
        <p>{t("veeamRequirements.access")}</p>
      </div>
    </div>
  );
}
