import { useState } from "react";
import { Panel } from "@/components/page/Panel";
import { Segmented } from "@/components/ui/segmented";
import { UsageBar } from "@/components/UsageBar";
import { formatPercent } from "@/lib/format";
import { usageLevel, USAGE_TEXT_CLASS } from "@/lib/usage-level";
import { cn } from "@/lib/utils";
import type { TopConsumer } from "@/types/ws";

interface TopConsumersProps {
  consumers: TopConsumer[];
  vmNames: Map<string, string>;
}

type SortField = "cpu" | "memory";

const SORT_OPTIONS = [
  { value: "cpu", label: "CPU" },
  { value: "memory", label: "Memory" },
] as const;

/** The busiest guests, ranked by the measure picked in the title row. */
export function TopConsumers({ consumers, vmNames }: TopConsumersProps) {
  const [sortBy, setSortBy] = useState<SortField>("cpu");

  const sorted = [...consumers].sort((a, b) => {
    if (sortBy === "cpu") return b.cpuPercent - a.cpuPercent;
    return b.memPercent - a.memPercent;
  });

  return (
    <Panel
      flush
      title="Top consumers"
      actions={
        <Segmented
          aria-label="Rank by"
          value={sortBy}
          onValueChange={setSortBy}
          options={SORT_OPTIONS}
        />
      }
      className="flex h-full flex-col overflow-hidden"
      bodyClassName="min-h-0 flex-1 overflow-y-auto"
    >
      {sorted.length === 0 ? (
        <p
          className="px-[18px] text-sm text-muted-foreground"
          data-testid="empty-consumers"
        >
          No guests are running.
        </p>
      ) : (
        <ol className="divide-y border-t" data-testid="consumer-list">
          {sorted.map((vm, i) => {
            const value = sortBy === "cpu" ? vm.cpuPercent : vm.memPercent;
            const level = usageLevel(value);
            return (
              <li
                key={vm.vmId}
                className="flex items-center gap-3 px-[18px] py-[7px] text-sm"
              >
                <span
                  aria-hidden="true"
                  className="w-4 shrink-0 text-[12.5px] text-muted-foreground/75"
                >
                  {i + 1}
                </span>
                <span className="min-w-0 flex-[1_1_9rem] truncate">
                  {vmNames.get(vm.vmId) ?? vm.vmId.slice(0, 8)}
                </span>
                <UsageBar percent={value} className="flex-[2_1_6rem]" />
                <span
                  className={cn(
                    "w-14 shrink-0 text-right",
                    level === "ok"
                      ? "text-muted-foreground"
                      : USAGE_TEXT_CLASS[level],
                  )}
                >
                  {formatPercent(value)}
                </span>
              </li>
            );
          })}
        </ol>
      )}
    </Panel>
  );
}
