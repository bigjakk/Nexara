/**
 * Tiny inline SVG sparkline for showing a single metric series in a card.
 * Avoids pulling in `victory-native`/`react-native-skia` (heavy + react 19
 * peer-dep nonsense). For a richer per-VM/node metric chart screen later
 * we can revisit a real charting lib.
 */

import { useMemo } from "react";
import { View } from "react-native";
import Svg, { Path, Polyline } from "react-native-svg";

export interface SparklineProps {
  /** Series of points already normalised against the value scale. */
  values: number[];
  /** Logical width in dp. */
  width?: number | undefined;
  /** Logical height in dp. */
  height?: number | undefined;
  /** Stroke colour. Default is the primary green. */
  color?: string | undefined;
  /** Optional fill — semi-transparent area under the line. */
  fillColor?: string | undefined;
  /** Override the y-axis range. Defaults to [min(values), max(values)]. */
  range?: [number, number] | undefined;
}

export function Sparkline({
  values,
  width = 280,
  height = 60,
  color = "#22c55e",
  fillColor = "rgba(34, 197, 94, 0.15)",
  range,
}: SparklineProps) {
  const { points, areaPath } = useMemo(() => {
    if (values.length === 0) {
      return { points: "", areaPath: "" };
    }

    const [minRaw, maxRaw] = range ?? [Math.min(...values), Math.max(...values)];
    const min = minRaw;
    const max = maxRaw === min ? min + 1 : maxRaw;

    const stepX = values.length > 1 ? width / (values.length - 1) : 0;

    const xy = values.map((v, i) => {
      const x = i * stepX;
      const norm = (v - min) / (max - min);
      const y = height - norm * height;
      return [x, y] as const;
    });

    const polyline = xy.map(([x, y]) => `${x},${y}`).join(" ");

    const last = xy[xy.length - 1];
    const first = xy[0];
    const area =
      first && last
        ? `M ${first[0]},${height} L ${xy
            .map(([x, y]) => `${x},${y}`)
            .join(" L ")} L ${last[0]},${height} Z`
        : "";

    return { points: polyline, areaPath: area };
  }, [values, width, height, range]);

  if (values.length === 0) {
    return <View style={{ width, height }} />;
  }

  return (
    <Svg width={width} height={height}>
      {areaPath ? <Path d={areaPath} fill={fillColor} stroke="none" /> : null}
      <Polyline
        points={points}
        fill="none"
        stroke={color}
        strokeWidth={2}
        strokeLinejoin="round"
        strokeLinecap="round"
      />
    </Svg>
  );
}
