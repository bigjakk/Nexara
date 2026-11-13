import { usageLevel } from "@/lib/usage-level";
import { cn } from "@/lib/utils";

interface UtilRingProps {
  /** 0-100, or null while no live metric has arrived. */
  percent: number | null;
  label: string;
}

const SIZE = 38;
const R = 15;
const CIRC = 2 * Math.PI * R;

/**
 * A small utilisation arc in the data colour, amber and red at the shared
 * thresholds, with the reading in its centre and a label underneath.
 */
export function UtilRing({ percent, label }: UtilRingProps) {
  const clamped = percent === null ? null : Math.min(100, Math.max(0, percent));
  const level = usageLevel(clamped);

  return (
    <div className="flex flex-col items-center gap-1">
      <svg width={SIZE} height={SIZE} aria-hidden="true">
        <circle
          cx={SIZE / 2}
          cy={SIZE / 2}
          r={R}
          fill="none"
          className="stroke-foreground/10"
          strokeWidth={3.5}
        />
        {clamped !== null && (
          <circle
            cx={SIZE / 2}
            cy={SIZE / 2}
            r={R}
            fill="none"
            className={cn(
              level === "err"
                ? "stroke-red-500"
                : level === "warn"
                  ? "stroke-amber-500"
                  : "stroke-data",
            )}
            strokeWidth={3.5}
            strokeLinecap="round"
            strokeDasharray={`${String((clamped / 100) * CIRC)} ${String(CIRC)}`}
            transform={`rotate(-90 ${String(SIZE / 2)} ${String(SIZE / 2)})`}
            style={{
              transition: "stroke-dasharray 0.7s cubic-bezier(0.2,0.8,0.2,1)",
            }}
          />
        )}
        <text
          x={SIZE / 2}
          y={SIZE / 2 + 3.5}
          textAnchor="middle"
          className="fill-foreground"
          fontSize={10}
          fontWeight={600}
        >
          {clamped === null ? "—" : String(Math.round(clamped))}
        </text>
      </svg>
      <span className="text-[10.5px] leading-none text-muted-foreground">
        {label}
      </span>
    </div>
  );
}
