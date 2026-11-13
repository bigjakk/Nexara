import { useMemo } from "react";
import { Button } from "@/components/ui/button";
import { Panel } from "@/components/page/Panel";
import { useHistory } from "@/features/history/api/history-queries";
import { historyExclusions } from "@/features/history/lib/history";
import { formatRelativeTime } from "@/lib/format";
import { guestHistoryKind, guestRowNaming } from "../lib/guest-history-kind";
import { guestHistoryTarget } from "../lib/guest-history-target";

/** How many entries the Overview shows before pointing at the History tab. */
const RECENT_COUNT = 5;

/**
 * The last few things that happened to a guest, on its Overview. Reads the
 * first page of the History tab with the tab's own default filters, so the
 * two share one cached read. Renders nothing while there is nothing to list.
 */
export function GuestRecentPanel({
  clusterId,
  vmid,
  onOpenHistory,
}: {
  clusterId: string;
  vmid: number;
  onOpenHistory: () => void;
}) {
  const target = useMemo(
    () => guestHistoryTarget(clusterId, vmid),
    [clusterId, vmid],
  );
  const timeWindow = guestHistoryKind.defaultWindow ?? "";
  const { data } = useHistory({
    target,
    page: 0,
    category: "",
    excludeCategories: historyExclusions(guestHistoryKind, false, ""),
    source: "",
    timeWindow,
    allTime: timeWindow === "" && guestHistoryKind.defaultWindow !== undefined,
    includeGuests: false,
    enabled: vmid > 0,
  });
  const entries = (data?.items ?? []).slice(0, RECENT_COUNT);
  if (entries.length === 0) return null;

  return (
    <Panel
      title="Recent on this guest"
      actions={
        <Button
          variant="link"
          size="sm"
          className="h-auto p-0"
          onClick={onOpenHistory}
        >
          History
        </Button>
      }
    >
      <ul className="space-y-2.5 text-sm">
        {entries.map((entry) => {
          const summary = guestRowNaming.summary(entry);
          return (
            <li key={entry.id} className="flex items-baseline gap-3">
              <span className="min-w-0 flex-1 truncate">
                <span className="font-medium">
                  {guestRowNaming.eventLabel(entry)}
                </span>
                {summary !== "" && (
                  <span className="text-muted-foreground"> {summary}</span>
                )}
              </span>
              <span className="shrink-0 text-[12.5px] text-muted-foreground/75">
                {formatRelativeTime(entry.occurred_at)}
              </span>
            </li>
          );
        })}
      </ul>
    </Panel>
  );
}
