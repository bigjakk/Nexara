import { Button } from "@/components/ui/button";

const ranges = [
  { key: "live", label: "Live", disabled: false },
  { key: "1h", label: "1h", disabled: true },
  { key: "6h", label: "6h", disabled: true },
  { key: "24h", label: "24h", disabled: true },
  { key: "7d", label: "7d", disabled: true },
] as const;

export function TimeRangeSelector() {
  return (
    <div className="flex items-center gap-1" data-testid="time-range-selector">
      {ranges.map((range) => (
        <div key={range.key} className="relative">
          <Button
            variant={range.key === "live" ? "default" : "ghost"}
            size="sm"
            className="h-7 px-2 text-xs"
            disabled={range.disabled}
            title={range.disabled ? "Coming soon" : undefined}
            data-testid={`range-${range.key}`}
          >
            {range.key === "live" && (
              <span className="mr-1.5 inline-block h-2 w-2 animate-pulse rounded-full bg-green-400" />
            )}
            {range.label}
          </Button>
        </div>
      ))}
    </div>
  );
}
