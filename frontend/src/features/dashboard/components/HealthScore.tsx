interface HealthScoreProps {
  score: number;
}

function getScoreColor(score: number): string {
  if (score >= 80) return "#22c55e"; // green
  if (score >= 60) return "#eab308"; // yellow
  if (score >= 40) return "#f97316"; // orange
  return "#ef4444"; // red
}

export function HealthScore({ score }: HealthScoreProps) {
  const color = getScoreColor(score);
  const radius = 40;
  const circumference = 2 * Math.PI * radius;
  const progress = (score / 100) * circumference;
  const dashOffset = circumference - progress;

  return (
    <div className="flex flex-col items-center gap-2" data-testid="health-score">
      <svg width="100" height="100" viewBox="0 0 100 100">
        {/* Background circle */}
        <circle
          cx="50"
          cy="50"
          r={radius}
          fill="none"
          stroke="hsl(var(--muted))"
          strokeWidth="8"
        />
        {/* Progress arc */}
        <circle
          cx="50"
          cy="50"
          r={radius}
          fill="none"
          stroke={color}
          strokeWidth="8"
          strokeLinecap="round"
          strokeDasharray={circumference}
          strokeDashoffset={dashOffset}
          transform="rotate(-90 50 50)"
        />
        {/* Center text */}
        <text
          x="50"
          y="50"
          textAnchor="middle"
          dominantBaseline="central"
          className="text-xl font-bold"
          fill="currentColor"
          fontSize="20"
          data-testid="health-score-value"
        >
          {score}
        </text>
      </svg>
      <span className="text-xs text-muted-foreground">Health Score</span>
    </div>
  );
}
