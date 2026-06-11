interface UtilRingProps {
  /** 0-100, or null while no live metric has arrived. */
  percent: number | null;
  baseColor: string;
  label: string;
}

const SIZE = 36;
const R = 14;
const CIRC = 2 * Math.PI * R;

/** Small utilization arc with the value centered and a label underneath. */
export function UtilRing({ percent, baseColor, label }: UtilRingProps) {
  const clamped =
    percent === null ? null : Math.min(100, Math.max(0, percent));
  const color =
    clamped === null
      ? "transparent"
      : clamped >= 90
        ? "#ef4444"
        : clamped >= 75
          ? "#f59e0b"
          : baseColor;

  return (
    <div className="flex flex-col items-center gap-0.5">
      <svg width={SIZE} height={SIZE} aria-hidden="true">
        <circle
          cx={SIZE / 2}
          cy={SIZE / 2}
          r={R}
          fill="none"
          stroke="hsl(var(--foreground) / 0.09)"
          strokeWidth={3.5}
        />
        {clamped !== null && (
          <circle
            cx={SIZE / 2}
            cy={SIZE / 2}
            r={R}
            fill="none"
            stroke={color}
            strokeWidth={3.5}
            strokeLinecap="round"
            strokeDasharray={`${String((clamped / 100) * CIRC)} ${String(CIRC)}`}
            transform={`rotate(-90 ${String(SIZE / 2)} ${String(SIZE / 2)})`}
            style={{ transition: "stroke-dasharray 0.7s cubic-bezier(0.2,0.8,0.2,1)" }}
          />
        )}
        <text
          x={SIZE / 2}
          y={SIZE / 2 + 3}
          textAnchor="middle"
          className="fill-foreground"
          fontSize={9}
          fontWeight={600}
        >
          {clamped === null ? "—" : String(Math.round(clamped))}
        </text>
      </svg>
      <span className="text-[10px] leading-none text-muted-foreground">
        {label}
      </span>
    </div>
  );
}
