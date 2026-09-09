import { useMemo, useState } from "react";
import { ChevronDown, ChevronRight, Thermometer } from "lucide-react";

import { Skeleton } from "@/components/ui/skeleton";
import { cn } from "@/lib/utils";
import {
  useNodeSensors,
  type NodeSensorReading,
} from "../../api/cluster-queries";

/**
 * Hardware temperatures for a node, read from its kernel hwmon tree over SSH.
 *
 * Proxmox has no sensor API, so this depends on the cluster's SSH credentials
 * and degrades to an explanatory note whenever they are missing, unusable, or
 * the host simply has no temperature-reporting chips (every virtualised node).
 * It is a supplementary panel on a page full of things that work without SSH,
 * so it never renders as an error.
 */
export function NodeTemperatures({
  clusterId,
  nodeName,
  online,
  className,
}: {
  clusterId: string;
  nodeName: string;
  /** An offline node cannot be reached over SSH; skip the guaranteed timeout. */
  online: boolean;
  className?: string;
}) {
  const { data, isLoading, isError } = useNodeSensors(
    clusterId,
    nodeName,
    online,
  );
  const groups = useMemo(() => groupByDevice(data?.items ?? []), [data?.items]);

  return (
    <div className={cn("rounded-lg border p-4", className)}>
      <div className="mb-3 flex items-center gap-2 text-sm font-medium">
        <span className="text-muted-foreground">
          <Thermometer className="h-4 w-4" />
        </span>
        Temperatures
      </div>
      <Body
        online={online}
        // A paused query — which is what a backgrounded tab produces — is
        // neither loading nor errored nor holding data, so "still loading" has
        // to mean "no data yet", not isLoading alone. Keying off isLoading
        // would render this panel empty rather than as a skeleton.
        loading={isLoading || (!data && !isError)}
        error={isError}
        available={data?.available ?? false}
        reason={data?.reason ?? ""}
        groups={groups}
      />
    </div>
  );
}

function Body({
  online,
  loading,
  error,
  available,
  reason,
  groups,
}: {
  online: boolean;
  loading: boolean;
  error: boolean;
  available: boolean;
  reason: string;
  groups: SensorGroup[];
}) {
  if (!online) {
    return (
      <Note>Temperatures are only readable while the node is online.</Note>
    );
  }
  if (loading) {
    return (
      <div className="space-y-2">
        <Skeleton className="h-5 w-full" />
        <Skeleton className="h-5 w-4/5" />
      </div>
    );
  }
  if (error) {
    return <Note>Could not load temperatures for this node.</Note>;
  }
  if (!available) {
    return <Note>{reason || "Temperatures are not available."}</Note>;
  }
  if (groups.length === 0) {
    return (
      <Note>
        This node reports no temperature sensors. Virtualised hosts usually have
        none.
      </Note>
    );
  }
  return (
    <div className="grid grid-cols-1 items-start gap-x-8 gap-y-2 sm:grid-cols-2 lg:grid-cols-3">
      {groups.map((group) => (
        <SensorGroupRow key={group.device} group={group} />
      ))}
    </div>
  );
}

function Note({ children }: { children: React.ReactNode }) {
  return <p className="text-sm text-muted-foreground">{children}</p>;
}

/**
 * One hwmon device, collapsed to its hottest reading.
 *
 * Collapsed by default because a modern CPU publishes a per-core sensor: a
 * 16-core host would otherwise push seventeen coretemp rows into a summary
 * card. The hottest reading is the one that answers "is anything too hot", and
 * the rest are one click away rather than hidden.
 */
function SensorGroupRow({ group }: { group: SensorGroup }) {
  const [expanded, setExpanded] = useState(false);
  const extra = group.readings.length - 1;

  return (
    <div>
      <div className="flex items-baseline justify-between gap-2 text-sm">
        <div className="flex min-w-0 items-baseline gap-1.5">
          {extra > 0 ? (
            <button
              type="button"
              onClick={() => {
                setExpanded((prev) => !prev);
              }}
              className="flex min-w-0 items-center gap-1 text-left hover:text-foreground"
              aria-expanded={expanded}
            >
              {expanded ? (
                <ChevronDown className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
              ) : (
                <ChevronRight className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
              )}
              <span className="truncate font-medium">{group.title}</span>
              <span className="shrink-0 text-xs text-muted-foreground">
                +{extra}
              </span>
            </button>
          ) : (
            // Same leading gap as the expandable rows. A single-sensor chip
            // stacked above a multi-sensor one in the same grid column would
            // otherwise sit a chevron's width to its left.
            <span className="flex min-w-0 items-center gap-1">
              <span aria-hidden className="h-3.5 w-3.5 shrink-0" />
              <span className="truncate font-medium">{group.title}</span>
            </span>
          )}
        </div>
        <Reading reading={group.hottest} />
      </div>
      {expanded && (
        <dl className="mt-1.5 space-y-1 border-l pl-4">
          {group.readings.map((reading) => (
            <div
              key={reading.key}
              className="flex items-baseline justify-between gap-2 text-sm"
            >
              <dt className="min-w-0 truncate text-muted-foreground">
                {sensorLabel(reading)}
              </dt>
              <dd className="shrink-0">
                <Reading reading={reading} />
              </dd>
            </div>
          ))}
        </dl>
      )}
    </div>
  );
}

/**
 * A temperature, coloured only when the chip itself published a threshold to
 * compare it against.
 *
 * No invented limits: hwmon drivers disagree wildly about what is hot, so a
 * hard-coded "amber above 70" would flag a perfectly healthy NVMe and stay
 * silent on a chipset sensor that is genuinely cooking. An uncoloured reading
 * means the driver offered no opinion, which is the honest rendering.
 */
function Reading({ reading }: { reading: NodeSensorReading }) {
  const critical =
    reading.crit_c !== undefined && reading.temp_c >= reading.crit_c;
  const high =
    !critical &&
    reading.high_c !== undefined &&
    reading.temp_c >= reading.high_c;

  const limits = [
    reading.high_c !== undefined ? `high ${formatTemp(reading.high_c)}` : "",
    reading.crit_c !== undefined
      ? `critical ${formatTemp(reading.crit_c)}`
      : "",
  ].filter(Boolean);

  return (
    <span
      className={cn(
        "shrink-0 text-right font-medium tabular-nums",
        critical && "text-destructive",
        high && "text-amber-600 dark:text-amber-500",
      )}
      title={limits.length > 0 ? limits.join(" · ") : undefined}
    >
      {formatTemp(reading.temp_c)}
    </span>
  );
}

function formatTemp(celsius: number): string {
  return `${celsius.toFixed(1)} °C`;
}

/** The kernel's own label, falling back to the sensor's sysfs name. */
function sensorLabel(reading: NodeSensorReading): string {
  return reading.label || reading.key;
}

interface SensorGroup {
  device: string;
  /** Chip name, disambiguated by device when the host has two of the same. */
  title: string;
  hottest: NodeSensorReading;
  readings: NodeSensorReading[];
}

/**
 * Groups readings by hwmon device, preserving the server's ordering.
 *
 * Grouping by device and not by chip name because a host with two NVMe drives
 * publishes "nvme" twice; merging those would silently report one drive's
 * temperature under both. The chip name gets the device appended in exactly
 * that case, and stays clean in the common one.
 */
function groupByDevice(readings: NodeSensorReading[]): SensorGroup[] {
  const byDevice = new Map<string, NodeSensorReading[]>();
  for (const reading of readings) {
    const existing = byDevice.get(reading.device);
    if (existing) {
      existing.push(reading);
    } else {
      byDevice.set(reading.device, [reading]);
    }
  }

  const chipCounts = new Map<string, number>();
  for (const group of byDevice.values()) {
    const chip = group[0]?.chip ?? "";
    chipCounts.set(chip, (chipCounts.get(chip) ?? 0) + 1);
  }

  const groups: SensorGroup[] = [];
  for (const [device, group] of byDevice) {
    const first = group[0];
    if (!first) continue;
    const ambiguous = (chipCounts.get(first.chip) ?? 0) > 1;
    groups.push({
      device,
      title: ambiguous ? `${first.chip} (${device})` : first.chip,
      hottest: group.reduce(
        (max, r) => (r.temp_c > max.temp_c ? r : max),
        first,
      ),
      readings: group,
    });
  }
  return groups;
}
