import { useEffect, useMemo, useRef, useState } from "react";
import { LIVE_WINDOW_MS, spliceLive } from "@/lib/metric-series";
import type {
  MetricDataPoint,
  MetricSeriesPoint,
  VmLiveMetric,
} from "@/types/ws";

const MAX_LIVE_POINTS = 60;

/**
 * A node's or guest's chart series: its stored history, or in live mode the
 * last hour of it with the WebSocket ticks appended. The WS store keeps only
 * each entity's latest snapshot, so the ticks accumulate here; a tick no newer
 * than the last one kept (StrictMode's dev double-mount re-running the effect,
 * or a frame for an older cycle) is not appended twice.
 */
export function useLiveSeries(
  history: readonly MetricSeriesPoint[] | undefined,
  liveMetric: VmLiveMetric | undefined,
  isLive: boolean,
): MetricSeriesPoint[] {
  const [liveHistory, setLiveHistory] = useState<MetricDataPoint[]>([]);
  const lastAppendedRef = useRef<VmLiveMetric | undefined>(undefined);
  useEffect(() => {
    if (!liveMetric || liveMetric === lastAppendedRef.current) return;
    lastAppendedRef.current = liveMetric;
    setLiveHistory((prev) => {
      const last = prev[prev.length - 1];
      if (last !== undefined && last.timestamp >= liveMetric.collectedAt) {
        return prev;
      }
      const next = [
        ...prev,
        {
          timestamp: liveMetric.collectedAt,
          cpuPercent: liveMetric.cpuPercent,
          memPercent: liveMetric.memPercent,
          diskReadBps: liveMetric.diskReadBps,
          diskWriteBps: liveMetric.diskWriteBps,
          netInBps: liveMetric.netInBps,
          netOutBps: liveMetric.netOutBps,
        },
      ];
      return next.length > MAX_LIVE_POINTS
        ? next.slice(next.length - MAX_LIVE_POINTS)
        : next;
    });
  }, [liveMetric]);

  return useMemo(() => {
    const seed = history ?? [];
    if (!isLive) return [...seed];
    return spliceLive(seed, liveHistory, LIVE_WINDOW_MS);
  }, [isLive, history, liveHistory]);
}
