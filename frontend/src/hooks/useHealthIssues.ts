import { useCallback, useMemo } from "react";
import { issueSig } from "@/lib/health-issues";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import { useUpsDevices } from "@/features/power/api/ups-queries";
import { usePermissions } from "@/hooks/usePermissions";
import { useHealthDismissStore } from "@/stores/health-dismiss-store";
import { useHealthMuteStore } from "@/stores/health-mute-store";
import {
  worstIssueSeverity,
  type CephSeverity,
} from "@/features/ceph/lib/ceph-health";
import type { HealthIssue } from "@/types/api";

/** An issue with where it came from, its dismissal signature and its page. */
export interface FlatIssue extends HealthIssue {
  clusterId: string;
  clusterName: string;
  sig: string;
  to: string;
}

/**
 * Where an issue's Open goes. A store's issue opens the cluster's Storage tab,
 * which lists the store with its state (the store's own page is keyed by an id
 * the issue does not carry); it opened the cluster's Overview, which does not
 * show stores at all.
 */
export function issueLink(clusterId: string, i: HealthIssue): string {
  if (i.type === "ceph") return `/clusters/${clusterId}?tab=ceph`;
  if (i.scope === "storage") return `/clusters/${clusterId}?tab=storage`;
  if (i.type === "ha_error") return `/clusters/${clusterId}?tab=ha`;
  return `/clusters/${clusterId}`;
}

export interface HealthIssues {
  /** Every issue the sources report, hidden ones included. */
  all: FlatIssue[];
  /** False until every source has answered; `all` is not yet the whole list. */
  loaded: boolean;
  /** What is neither dismissed nor of a muted type. */
  visible: FlatIssue[];
  hiddenCount: number;
  mutedTypes: string[];
  /** The worst severity among the visible issues; "ok" when there are none. */
  overall: CephSeverity;
  dismiss: (sig: string) => void;
  mute: (type: string) => void;
  restoreAll: () => void;
}

/**
 * The open infrastructure issues (Ceph, HA, disks, storage, failed tasks, …)
 * the server attaches to each cluster, and each UPS's power issues for view:ups
 * holders, with this browser's dismissals and muted types applied. The header
 * pill and the dashboard's list both read it, so they always agree.
 */
export function useHealthIssues(): HealthIssues {
  const { data: clusters } = useClusters();
  const { hasGlobalPermission } = usePermissions();
  const canViewUps = hasGlobalPermission("view", "ups");
  const { data: upsDevices } = useUpsDevices(canViewUps);
  const dismissed = useHealthDismissStore((s) => s.dismissed);
  const dismiss = useHealthDismissStore((s) => s.dismiss);
  const restoreDismissed = useHealthDismissStore((s) => s.restoreAll);
  const mutedTypes = useHealthMuteStore((s) => s.mutedTypes);
  const mute = useHealthMuteStore((s) => s.mute);
  const restoreMuted = useHealthMuteStore((s) => s.restoreAll);

  const all = useMemo<FlatIssue[]>(
    () => [
      ...(clusters ?? []).flatMap((c) =>
        (c.issues ?? []).map((iss) => ({
          ...iss,
          clusterId: c.id,
          clusterName: c.name,
          sig: issueSig(c.id, iss),
          to: issueLink(c.id, iss),
        })),
      ),
      ...(canViewUps ? (upsDevices ?? []) : []).flatMap((d) =>
        d.issues.map((iss) => ({
          ...iss,
          clusterId: `ups:${d.id}`,
          clusterName: d.server_name,
          // The detail carries the charge and runtime left, which change on
          // every poll; signed on the summary instead, a dismissal holds until
          // the issue resolves, and "Low battery" turning into "Shutting down"
          // still shows.
          sig: issueSig(`ups:${d.id}`, { ...iss, detail: iss.summary }),
          to: `/power/${d.id}`,
        })),
      ),
    ],
    [clusters, canViewUps, upsDevices],
  );

  const visible = useMemo(() => {
    const dismissedSet = new Set(dismissed);
    const mutedSet = new Set(mutedTypes);
    return all.filter((i) => !mutedSet.has(i.type) && !dismissedSet.has(i.sig));
  }, [all, dismissed, mutedTypes]);

  const restoreAll = useCallback(() => {
    restoreDismissed();
    restoreMuted();
  }, [restoreDismissed, restoreMuted]);

  return {
    all,
    loaded: clusters !== undefined && (!canViewUps || upsDevices !== undefined),
    visible,
    hiddenCount: all.length - visible.length,
    mutedTypes,
    overall: worstIssueSeverity(visible),
    dismiss,
    mute,
    restoreAll,
  };
}
