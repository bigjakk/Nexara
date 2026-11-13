import { useId, useState } from "react";
import {
  ResponsiveContainer,
  AreaChart,
  Area,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
} from "recharts";
import { Panel } from "@/components/page/Panel";
import { Stat, StatStrip } from "@/components/page/StatStrip";
import { Segmented } from "@/components/ui/segmented";
import { DATA_COLOR } from "@/lib/chart-colors";
import { formatBytes, formatPercent } from "@/lib/format";
import { usageLevel } from "@/lib/usage-level";
import { useVeeamRepositoryMetrics } from "../api/backup-queries";
import {
  AXIS_TICK,
  TOOLTIP_CURSOR,
  TOOLTIP_STYLE,
  unbroken,
} from "../components/chart-style";
import type { VeeamRepository } from "../types/backup";

const RANGES = ["24h", "7d", "30d", "90d"] as const;
type Range = (typeof RANGES)[number];

interface VeeamRepositoryPanelsProps {
  serverId: string;
  repositories: VeeamRepository[];
}

/**
 * A Veeam server's repositories, one panel each: how full it is, then its
 * used space over time.
 *
 * Repositories are a GLOBAL resource, not a per-cluster one: a single
 * repository holds the backups of every cluster the Veeam server protects.
 * That is why they are not filtered by cluster anywhere in this feature.
 */
export function VeeamRepositoryPanels({
  serverId,
  repositories,
}: VeeamRepositoryPanelsProps) {
  if (repositories.length === 0) {
    return (
      <Panel>
        <p className="py-6 text-center text-sm text-muted-foreground">
          No repositories reported by this server.
        </p>
      </Panel>
    );
  }

  return (
    <>
      {repositories.map((repo) => (
        <RepositoryPanel key={repo.id} serverId={serverId} repository={repo} />
      ))}
    </>
  );
}

function RepositoryPanel({
  serverId,
  repository: repo,
}: {
  serverId: string;
  repository: VeeamRepository;
}) {
  const [range, setRange] = useState<Range>("7d");
  const gradientId = useId();
  // Keyed on the repository's VEEAM id, not Nexara's row id: the samples are
  // stored against Veeam's identity so they survive the local row being
  // re-created.
  const { data, isLoading } = useVeeamRepositoryMetrics(
    serverId,
    repo.veeam_id,
    range,
  );
  const metrics = data ?? [];

  // Veeam reports capacity as floating-point GB and the client converts
  // to bytes; a target it cannot measure comes back as zero, which must
  // read as "unknown" rather than as a full disk.
  const known = repo.capacity_bytes > 0;
  const usedPct = known
    ? Math.min((repo.used_bytes / repo.capacity_bytes) * 100, 100)
    : null;
  const level = usageLevel(usedPct);

  const chartData = metrics.map((m) => ({
    time: new Date(m.time).toLocaleString([], {
      month: "short",
      day: "numeric",
      hour: "2-digit",
      minute: "2-digit",
    }),
    used: m.used_bytes,
    capacity: m.capacity_bytes,
  }));

  // Nothing to plot yet: the collector writes one sample per inventory pass,
  // so a freshly added server has none. An empty axis would look like a broken
  // chart, not a young one, so the chart and its range wait for a sample. The
  // range stays while another window loads, or it could not be changed back.
  const hasChart = chartData.length > 0;
  const showRange = hasChart || isLoading || range !== "7d";

  return (
    <Panel
      flush
      title={<span className="break-all">{repo.name}</span>}
      aside={repo.type}
      actions={
        showRange ? (
          <Segmented
            aria-label={`Time range for ${repo.name}`}
            value={range}
            onValueChange={setRange}
            options={RANGES.map((r) => ({ value: r, label: r }))}
          />
        ) : undefined
      }
    >
      <StatStrip className="pb-1">
        {known ? (
          <>
            <Stat
              label="Used"
              value={formatBytes(repo.used_bytes)}
              sub={
                usedPct === null
                  ? undefined
                  : `${formatPercent(usedPct)} of ${formatBytes(repo.capacity_bytes)}`
              }
              percent={usedPct}
              tone={
                level === "err" ? "bad" : level === "warn" ? "warn" : "default"
              }
            />
            <Stat label="Free" value={formatBytes(repo.free_bytes)} />
          </>
        ) : (
          <Stat
            label="Capacity"
            value="Unknown"
            sub="Veeam reports no capacity for this target."
            tone="quiet"
          />
        )}
        <Stat
          label="State"
          value={repo.is_online ? "Online" : "Offline"}
          sub={repo.host_name !== "" ? `on ${repo.host_name}` : undefined}
          tone={repo.is_online ? "default" : "bad"}
        />
      </StatStrip>

      {repo.path !== "" && (
        <p className="break-all px-[18px] pt-3 font-mono text-xs text-muted-foreground">
          {repo.path}
        </p>
      )}

      {hasChart && (
        <div className="mt-4 border-t px-[18px] pt-4">
          <p className="pb-2 text-xs font-medium text-muted-foreground">
            Used space
          </p>
          <div className="h-52">
            <ResponsiveContainer width="100%" height="100%">
              <AreaChart
                data={chartData}
                margin={{ top: 4, right: 16, bottom: 0, left: 0 }}
              >
                <defs>
                  <linearGradient id={gradientId} x1="0" y1="0" x2="0" y2="1">
                    <stop
                      offset="5%"
                      stopColor={DATA_COLOR}
                      stopOpacity={0.25}
                    />
                    <stop offset="95%" stopColor={DATA_COLOR} stopOpacity={0} />
                  </linearGradient>
                </defs>
                <CartesianGrid
                  strokeDasharray="3 4"
                  className="stroke-border"
                  vertical={false}
                />
                <XAxis
                  dataKey="time"
                  tick={AXIS_TICK}
                  axisLine={false}
                  tickLine={false}
                  minTickGap={48}
                />
                <YAxis
                  tickFormatter={(v: number) => unbroken(formatBytes(v))}
                  tick={AXIS_TICK}
                  axisLine={false}
                  tickLine={false}
                  width={64}
                />
                <Tooltip
                  formatter={(value: unknown) =>
                    typeof value === "number" ? formatBytes(value) : "0"
                  }
                  cursor={TOOLTIP_CURSOR}
                  contentStyle={TOOLTIP_STYLE}
                />
                <Area
                  type="monotone"
                  dataKey="used"
                  name="Used"
                  stroke={DATA_COLOR}
                  strokeWidth={2}
                  fill={`url(#${gradientId})`}
                  isAnimationActive={false}
                  activeDot={{ r: 3, strokeWidth: 0 }}
                />
              </AreaChart>
            </ResponsiveContainer>
          </div>
        </div>
      )}
    </Panel>
  );
}
