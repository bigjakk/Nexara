import { Info } from "lucide-react";

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
  return (
    <div className="panel flex gap-2.5 rounded-[10px] px-[18px] py-3 text-[12.5px] text-muted-foreground">
      <Info className="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
      <div className="space-y-1">
        <p>
          Requires <strong>Veeam Backup &amp; Replication 13.1 or newer</strong>{" "}
          with an <strong>Enterprise Plus</strong> licence. Earlier builds do
          not report Proxmox job state usably, and Community Edition cannot back
          up Proxmox VE at all.
        </p>
        <p>
          Nexara authenticates as a single administrator account. It reads job,
          session and restore-point state; it does not create or edit Veeam
          jobs.
        </p>
      </div>
    </div>
  );
}
