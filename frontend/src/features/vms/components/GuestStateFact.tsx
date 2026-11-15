import { useTranslation } from "react-i18next";
import { StatusFact } from "@/components/page/DetailHeader";
import { haltedState } from "@/lib/guest-state";
import { formatUptime } from "@/lib/format";

/**
 * The guest page's state fact: a halted VM's QEMU state, with what to do
 * about it, in place of "Running"; otherwise the status and its uptime.
 */
export function GuestStateFact({
  status,
  runState,
  uptime,
}: {
  status: string;
  runState: string | undefined;
  uptime: number;
}) {
  const { t } = useTranslation("vms");
  const halted = haltedState(status, runState);
  if (halted) {
    return (
      <StatusFact
        status={halted}
        label={t(`runState.${halted}`)}
        detail={
          <span className="text-muted-foreground">
            {t(`runStateHint.${halted}`)}
          </span>
        }
      />
    );
  }
  const normalized = status.toLowerCase();
  return (
    <StatusFact
      status={normalized}
      label={normalized.charAt(0).toUpperCase() + normalized.slice(1)}
      detail={
        normalized === "running" && uptime > 0
          ? `for ${formatUptime(uptime)}`
          : undefined
      }
    />
  );
}
