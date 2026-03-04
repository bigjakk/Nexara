import { Timer } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { useMetricStore } from "@/stores/metric-store";

const options = [
  { label: "5s", value: 5_000 },
  { label: "10s", value: 10_000 },
  { label: "30s", value: 30_000 },
  { label: "60s", value: 60_000 },
] as const;

export function RefreshRateSelector() {
  const refreshInterval = useMetricStore((s) => s.refreshInterval);
  const setRefreshInterval = useMetricStore((s) => s.setRefreshInterval);

  const currentLabel =
    options.find((o) => o.value === refreshInterval)?.label ?? `${String(refreshInterval / 1000)}s`;

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          variant="ghost"
          size="sm"
          className="h-7 gap-1.5 px-2 text-xs"
          data-testid="refresh-rate-selector"
        >
          <Timer className="h-3.5 w-3.5" />
          {currentLabel}
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <DropdownMenuRadioGroup
          value={String(refreshInterval)}
          onValueChange={(v) => { setRefreshInterval(Number(v)); }}
        >
          {options.map((opt) => (
            <DropdownMenuRadioItem
              key={opt.value}
              value={String(opt.value)}
              data-testid={`refresh-${String(opt.value)}`}
            >
              {opt.label}
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
