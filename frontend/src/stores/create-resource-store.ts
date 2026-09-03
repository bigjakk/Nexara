import { create } from "zustand";

export type CreateKind = "vm" | "ct" | "import";

interface CreateResourceState {
  // The create dialog currently open (null = none).
  dialog: CreateKind | null;
  // Cluster the open dialog targets.
  clusterId: string;
  // A create request awaiting a cluster choice (multi-cluster installs).
  pendingType: CreateKind | null;
}

interface CreateResourceActions {
  // Begin a create flow. Pass clusterId when the caller already knows it
  // (e.g. a context menu on a specific cluster); omit it to let the globally
  // mounted CreateResourceDialogs resolve the cluster (auto for single-cluster,
  // picker for multi-cluster).
  request: (type: CreateKind, clusterId?: string) => void;
  pickCluster: (clusterId: string) => void;
  cancelPending: () => void;
  close: () => void;
}

export const useCreateResourceStore = create<
  CreateResourceState & CreateResourceActions
>()((set, get) => ({
  dialog: null,
  clusterId: "",
  pendingType: null,

  request: (type, clusterId) => {
    if (clusterId) {
      set({ dialog: type, clusterId, pendingType: null });
    } else {
      set({ pendingType: type });
    }
  },
  pickCluster: (clusterId) => {
    const pending = get().pendingType;
    if (pending) set({ dialog: pending, clusterId, pendingType: null });
  },
  cancelPending: () => {
    set({ pendingType: null });
  },
  close: () => {
    set({ dialog: null });
  },
}));
