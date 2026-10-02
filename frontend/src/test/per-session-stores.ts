import { useConsoleStore } from "@/stores/console-store";
import { useCreateResourceStore } from "@/stores/create-resource-store";
import { useHealthDismissStore } from "@/stores/health-dismiss-store";
import { useMetricStore } from "@/stores/metric-store";
import { useTaskLogStore } from "@/stores/task-log-store";
import { useVMContextMenuStore } from "@/stores/vm-context-menu-store";

/**
 * A way to put something of the signed-in user's in a store, and to ask whether
 * any of it is still there. Asking and putting go through the store's own
 * actions, so a probe cannot drift from what the store really holds.
 */
export interface StoreProbe {
  dirty: () => void;
  holdsData: () => boolean;
}

/**
 * One probe per store that stores/session-reset.ts resets, keyed by its file
 * name in src/stores. session-reset.test.ts holds the table of every store and
 * checks that this one is exactly the "reset" half of it; auth-store.test.tsx
 * runs each probe through every path that ends a session.
 */
export const PER_SESSION_STORES: Record<string, StoreProbe> = {
  "console-store.ts": {
    dirty: () => {
      useConsoleStore.getState().addTab({
        clusterID: "cluster01",
        node: "pve-01",
        type: "node_shell",
        label: "pve-01 shell",
      });
    },
    holdsData: () => {
      const s = useConsoleStore.getState();
      return (
        s.tabs.length > 0 || s.activeTabId !== null || s.windowMode !== "hidden"
      );
    },
  },
  "task-log-store.ts": {
    dirty: () => {
      useTaskLogStore.getState().setFocusedTask({
        clusterId: "cluster01",
        upid: "UPID:pve-01:00000001:00000002:00000003:qmstart:101:admin@example.com:",
        description: "Start linux01",
      });
    },
    holdsData: () => useTaskLogStore.getState().focusedTask !== null,
  },
  "metric-store.ts": {
    dirty: () => {
      useMetricStore.getState().processMetricMessage("cluster01", {
        cluster_id: "cluster01",
        collected_at: "2026-01-01T00:00:00Z",
        node_count: 0,
        vm_count: 0,
        nodes: [],
        vms: [],
      });
    },
    holdsData: () => {
      const s = useMetricStore.getState();
      return s.metrics.size > 0 || s.lastProcessed.size > 0;
    },
  },
  "health-dismiss-store.ts": {
    dirty: () => {
      useHealthDismissStore
        .getState()
        .dismiss("cluster01|node_offline|pve-01|critical|pve-01 is offline");
    },
    holdsData: () => useHealthDismissStore.getState().dismissed.length > 0,
  },
  "vm-context-menu-store.ts": {
    dirty: () => {
      useVMContextMenuStore.getState().openDestroy({
        clusterId: "cluster01",
        resourceId: "resource-01",
        vmid: 101,
        name: "linux01",
        kind: "vm",
        status: "running",
        currentNode: "pve-01",
      });
    },
    holdsData: () => {
      const s = useVMContextMenuStore.getState();
      return (
        s.target !== null ||
        s.openDialog !== null ||
        s.confirmAction !== null ||
        s.confirmActionLabel !== null
      );
    },
  },
  "create-resource-store.ts": {
    dirty: () => {
      // Both halves: a dialog open on a cluster, and a request still waiting
      // for the cluster to be picked.
      useCreateResourceStore.getState().request("vm", "cluster01");
      useCreateResourceStore.getState().request("ct");
    },
    holdsData: () => {
      const s = useCreateResourceStore.getState();
      return s.dialog !== null || s.clusterId !== "" || s.pendingType !== null;
    },
  },
};

/**
 * Every per-session store back to its first state, written out rather than
 * through the stores' own reset actions: a test that sets up with the code it
 * is about to check would not notice that code stop working.
 */
export function emptyPerSessionStores(): void {
  useConsoleStore.setState({
    tabs: [],
    activeTabId: null,
    windowMode: "hidden",
  });
  useTaskLogStore.setState({ focusedTask: null });
  useMetricStore.setState({
    metrics: new Map(),
    version: 0,
    lastProcessed: new Map(),
  });
  useHealthDismissStore.setState({ dismissed: [] });
  useVMContextMenuStore.setState({
    target: null,
    openDialog: null,
    confirmAction: null,
    confirmActionLabel: null,
  });
  useCreateResourceStore.setState({
    dialog: null,
    clusterId: "",
    pendingType: null,
  });
}
