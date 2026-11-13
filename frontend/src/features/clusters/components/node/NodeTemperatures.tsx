import { useMemo, useState } from "react";
import { ChevronDown, ChevronRight } from "lucide-react";

import { Panel } from "@/components/page/Panel";
import { cn } from "@/lib/utils";
import {
  useNodeSensors,
  type NodeSensorReading,
} from "../../api/cluster-queries";

/**
 * Hardware temperatures for a node, read from its kernel hwmon tree over SSH.
 *
 * Proxmox has no sensor API, so this depends on the cluster's SSH credentials.
 * The panel is drawn while the read is out and when the node has readings; why
 * it has none is NodeTemperaturesNote's to say, as one line of the Hardware
 * panel. Both read the same query, so the page asks once.
 */
export function NodeTemperatures({
  clusterId,
  nodeName,
  online,
}: {
  clusterId: string;
  nodeName: string;
  /** An offline node cannot be reached over SSH; skip the guaranteed timeout. */
  online: boolean;
}) {
  const { data, isError } = useNodeSensors(clusterId, nodeName, online);
  const groups = useMemo(() => groupByDevice(data?.items ?? []), [data?.items]);
  if (!online) return null;
  // A query with neither data nor an error is loading, or paused in a
  // background tab: the panel holds its place until the read lands.
  if (data === undefined) {
    return isError ? null : (
      <Panel title="Temperatures">
        <p className="text-sm text-muted-foreground">Reading…</p>
      </Panel>
    );
  }
  if (!data.available || groups.length === 0) return null;

  return (
    <Panel title="Temperatures">
      <div className="grid grid-cols-[repeat(auto-fit,minmax(min(13rem,100%),1fr))] items-start gap-x-8 gap-y-2.5">
        {groups.map((group) => (
          <SensorGroupRow key={group.device} group={group} />
        ))}
      </div>
    </Panel>
  );
}

/**
 * Why the node page has no Temperatures panel: the credentials are missing or
 * unusable, the read failed, or the host has no temperature-reporting chips
 * (every virtualised node). It is supplementary on a page full of things that
 * work without SSH, so it is a note and never an error. Nothing while the read
 * is out, and nothing once there are readings.
 */
export function NodeTemperaturesNote({
  clusterId,
  nodeName,
  online,
}: {
  clusterId: string;
  nodeName: string;
  online: boolean;
}) {
  const { data, isError } = useNodeSensors(clusterId, nodeName, online);
  if (!online) {
    return (
      <Note>Temperatures are only readable while the node is online.</Note>
    );
  }
  if (data === undefined) {
    return isError ? (
      <Note>Could not load temperatures for this node.</Note>
    ) : null;
  }
  if (!data.available) {
    return <Note>{data.reason || "Temperatures are not available."}</Note>;
  }
  if (data.items.length === 0) {
    return (
      <Note>
        This node reports no temperature sensors. Virtualised hosts usually have
        none.
      </Note>
    );
  }
  return null;
}

function Note({ children }: { children: React.ReactNode }) {
  return (
    <div className="text-[12.5px]">
      <p className="font-medium">Temperatures</p>
      <p className="text-muted-foreground">{children}</p>
    </div>
  );
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
        {extra > 0 ? (
          // The chevron trails the name, so every device's name starts on the
          // panel's gutter whether or not it has more sensors to show.
          <button
            type="button"
            onClick={() => {
              setExpanded((prev) => !prev);
            }}
            className="flex min-w-0 items-center gap-1.5 rounded-sm text-left outline-hidden focus-visible:ring-2 focus-visible:ring-ring"
            aria-expanded={expanded}
          >
            <span className="truncate">{group.title}</span>
            <span className="shrink-0 text-[12.5px] text-muted-foreground">
              +{extra}
            </span>
            {expanded ? (
              <ChevronDown className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
            ) : (
              <ChevronRight className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
            )}
          </button>
        ) : (
          <span className="min-w-0 truncate">{group.title}</span>
        )}
        <Reading reading={group.hottest} />
      </div>
      {expanded && (
        <dl className="mt-1.5 space-y-1 border-l pl-3">
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
        high && "text-amber-700 dark:text-amber-400",
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
