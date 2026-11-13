import { Segmented } from "@/components/ui/segmented";
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
    <div className="inline-flex max-w-full" data-testid="time-range-selector">
      <Segmented
        aria-label="Time range"
        value={value}
        onValueChange={onChange}
        options={ranges.map((range) => ({
          value: range,
          testId: `range-${range}`,
          label:
            range === "live" ? (
              <>
                <span
                  aria-hidden="true"
                  className="h-1.5 w-1.5 rounded-full bg-emerald-500"
                />
                {labels[range]}
              </>
            ) : (
              labels[range]
            ),
        }))}
      />
    </div>
  );
}
