import { Button } from "@/components/ui/button";
import type { TimeRange } from "@/types/api";

const ranges: readonly TimeRange[] = ["live", "1h", "6h", "24h", "7d"];

const labels: Record<TimeRange, string> = {
  live: "Live",
  "1h": "1h",
  "6h": "6h",
  "24h": "24h",
  "7d": "7d",
};

interface TimeRangeSelectorProps {
  value: TimeRange;
  onChange: (range: TimeRange) => void;
}

export function TimeRangeSelector({ value, onChange }: TimeRangeSelectorProps) {
  return (
    <div className="flex items-center gap-1" data-testid="time-range-selector">
      {ranges.map((range) => (
        <div key={range} className="relative">
          <Button
            variant={range === value ? "default" : "ghost"}
            size="sm"
            className="h-7 px-2 text-xs"
            onClick={() => { onChange(range); }}
            data-testid={`range-${range}`}
          >
            {range === "live" && (
              <span className="mr-1.5 inline-block h-2 w-2 animate-pulse rounded-full bg-green-400" />
            )}
            {labels[range]}
          </Button>
        </div>
      ))}
    </div>
  );
}
