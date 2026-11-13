import { useState } from "react";
import {
  ChevronLeft,
  ChevronRight,
  ChevronsLeft,
  ChevronsRight,
  Loader2,
} from "lucide-react";

import { Panel } from "@/components/page/Panel";
import { QueryStateNotice } from "@/components/QueryStateNotice";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Segmented } from "@/components/ui/segmented";
import { useNodeSyslog } from "../../api/cluster-queries";

// Sent to the API as relative offsets (`24h`), which it resolves server-side.
//
// These used to be rendered into a `YYYY-MM-DD` date computed from the
// BROWSER's clock, and the node reads that string in its OWN timezone — so on
// any node behind the browser, the "Today" preset asked journalctl for a window
// starting in the future and got back its literal "-- No entries --", which
// reads as a node with no logs. A relative offset carries no wall-clock date,
// so there is no timezone to disagree about.
//
// "Today" is gone with it: it was the preset that tripped the bug, and it
// duplicated "Last 24h" anyway. "Last 1h" is the genuinely shorter window it
// never provided.
const SYSLOG_TIMESPANS = [
  { value: "1", label: "1h", ariaLabel: "Last hour" },
  { value: "24", label: "24h", ariaLabel: "Last 24 hours" },
  { value: "72", label: "3d", ariaLabel: "Last 3 days" },
  { value: "168", label: "7d", ariaLabel: "Last week" },
] as const;

type SyslogTimespan = (typeof SYSLOG_TIMESPANS)[number]["value"];

const SYSLOG_PAGE_SIZE = 500;

export function SyslogTab({
  clusterId,
  nodeName,
}: {
  clusterId: string;
  nodeName: string;
}) {
  const [serviceFilter, setServiceFilter] = useState("");
  const [timespanHours, setTimespanHours] = useState<SyslogTimespan>("24");
  // null page = "newest" (server resolves to last page)
  const [page, setPage] = useState<number | null>(null);
  const since = `${timespanHours}h`;

  const startParam = page === null ? undefined : page * SYSLOG_PAGE_SIZE;
  const syslogQuery = useNodeSyslog(clusterId, nodeName, {
    limit: SYSLOG_PAGE_SIZE,
    start: startParam,
    service: serviceFilter || undefined,
    since,
  });
  const { data, isFetching } = syslogQuery;
  const entries = data?.items;
  const total = data?.total ?? 0;
  const totalPages = Math.max(1, Math.ceil(total / SYSLOG_PAGE_SIZE));
  // When page is null (newest), we're on the last page
  const currentPage = page ?? totalPages - 1;
  const rangeStart = currentPage * SYSLOG_PAGE_SIZE + 1;
  const rangeEnd = Math.min(rangeStart + (entries?.length ?? 0) - 1, total);

  const goToPage = (p: number) => {
    const lastPage = totalPages - 1;
    if (p >= lastPage) {
      setPage(null); // use "newest" mode for last page
    } else {
      setPage(Math.max(0, p));
    }
  };

  // Reset to newest page when timespan or filter changes
  const handleTimespanChange = (hours: SyslogTimespan) => {
    setTimespanHours(hours);
    setPage(null);
  };

  return (
    <Panel
      flush
      title="System log"
      aside={
        isFetching ? (
          <span className="flex items-center gap-1.5">
            <Loader2 className="h-3.5 w-3.5 animate-spin" />
            Loading logs…
          </span>
        ) : total > 0 ? (
          `${total.toLocaleString()} ${total === 1 ? "entry" : "entries"}`
        ) : undefined
      }
      actions={
        <>
          <Segmented
            aria-label="Time span"
            value={timespanHours}
            onValueChange={handleTimespanChange}
            options={SYSLOG_TIMESPANS}
          />
          <Input
            aria-label="Filter by service"
            className="h-8 w-44"
            placeholder="Filter by service..."
            value={serviceFilter}
            onChange={(e) => {
              setServiceFilter(e.target.value);
            }}
          />
        </>
      }
    >
      {!entries || entries.length === 0 ? (
        <div className="px-[18px] pb-1">
          <QueryStateNotice
            query={syslogQuery}
            subject="this node's syslog"
            empty="No syslog entries found."
            skeletonClassName="h-64 w-full"
          />
        </div>
      ) : (
        <>
          <div className="max-h-[600px] overflow-auto border-y bg-foreground/[0.03] px-[18px] py-3">
            <pre className="text-xs leading-relaxed">
              {entries.map((entry) => (
                <div key={entry.n} className="hover:bg-foreground/[0.06]">
                  {entry.t}
                </div>
              ))}
            </pre>
          </div>
          {total > SYSLOG_PAGE_SIZE && (
            <div className="flex flex-wrap items-center justify-between gap-2 px-[18px] pt-3">
              <span className="text-[12.5px] text-muted-foreground">
                {rangeStart.toLocaleString()}–{rangeEnd.toLocaleString()} of{" "}
                {total.toLocaleString()} entries
              </span>
              <div className="flex items-center gap-1">
                <Button
                  aria-label="First page"
                  variant="ghost"
                  size="icon"
                  className="h-7 w-7"
                  disabled={currentPage === 0 || isFetching}
                  onClick={() => {
                    goToPage(0);
                  }}
                >
                  <ChevronsLeft className="h-4 w-4" />
                </Button>
                <Button
                  aria-label="Previous page"
                  variant="ghost"
                  size="icon"
                  className="h-7 w-7"
                  disabled={currentPage === 0 || isFetching}
                  onClick={() => {
                    goToPage(currentPage - 1);
                  }}
                >
                  <ChevronLeft className="h-4 w-4" />
                </Button>
                <span className="px-2 text-[12.5px] text-muted-foreground">
                  Page {(currentPage + 1).toLocaleString()} of{" "}
                  {totalPages.toLocaleString()}
                </span>
                <Button
                  aria-label="Next page"
                  variant="ghost"
                  size="icon"
                  className="h-7 w-7"
                  disabled={currentPage >= totalPages - 1 || isFetching}
                  onClick={() => {
                    goToPage(currentPage + 1);
                  }}
                >
                  <ChevronRight className="h-4 w-4" />
                </Button>
                <Button
                  aria-label="Last page"
                  variant="ghost"
                  size="icon"
                  className="h-7 w-7"
                  disabled={page === null || isFetching}
                  onClick={() => {
                    goToPage(totalPages - 1);
                  }}
                >
                  <ChevronsRight className="h-4 w-4" />
                </Button>
              </div>
            </div>
          )}
        </>
      )}
    </Panel>
  );
}
