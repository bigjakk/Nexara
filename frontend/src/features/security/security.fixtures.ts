import type { CVEScan, SecurityPosture } from "@/types/api";

/**
 * Shared fixtures for the Security tests. Test-only: nothing outside a
 * `*.test.ts(x)` imports it.
 */

/** A cluster's posture after a scan of all three nodes that found nothing. */
export function posture(over: Partial<SecurityPosture> = {}): SecurityPosture {
  return {
    scan_id: "s1",
    status: "completed",
    total_vulns: 0,
    critical_count: 0,
    high_count: 0,
    medium_count: 0,
    low_count: 0,
    unknown_count: 0,
    kev_count: 0,
    act_count: 0,
    attend_count: 0,
    track_star_count: 0,
    track_count: 0,
    total_nodes: 3,
    scanned_nodes: 3,
    posture_score: 100,
    started_at: "2026-01-05T00:00:00Z",
    ...over,
  };
}

/** A finished scan of all three nodes of cluster c1. */
export function cveScan(over: Partial<CVEScan> = {}): CVEScan {
  return {
    id: "scan-1",
    cluster_id: "c1",
    status: "completed",
    total_nodes: 3,
    scanned_nodes: 3,
    total_vulns: 0,
    critical_count: 0,
    high_count: 0,
    medium_count: 0,
    low_count: 0,
    started_at: "2026-01-05T00:00:00Z",
    completed_at: "2026-01-05T00:05:00Z",
    created_at: "2026-01-05T00:00:00Z",
    ...over,
  };
}
