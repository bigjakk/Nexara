import { create } from "zustand";
import type { ResourceKind } from "@/features/vms/types/vm";

export interface VMContextTarget {
  clusterId: string;
  resourceId: string;
  vmid: number;
  name: string;
  kind: ResourceKind;
  status: string;
  currentNode: string;
}

type OpenDialog = "clone" | "migrate" | "destroy" | "confirm-action" | null;

interface VMContextMenuState {
  target: VMContextTarget | null;
  openDialog: OpenDialog;
  confirmAction: string | null;
  confirmActionLabel: string | null;
}

interface VMContextMenuActions {
  openClone: (target: VMContextTarget) => void;
  openMigrate: (target: VMContextTarget) => void;
  openDestroy: (target: VMContextTarget) => void;
  openConfirmAction: (target: VMContextTarget, action: string, label: string) => void;
  closeDialog: () => void;
}

export const useVMContextMenuStore = create<VMContextMenuState & VMContextMenuActions>()(
  (set) => ({
    target: null,
    openDialog: null,
    confirmAction: null,
    confirmActionLabel: null,

    openClone: (target) => { set({ target, openDialog: "clone" }); },
    openMigrate: (target) => { set({ target, openDialog: "migrate" }); },
    openDestroy: (target) => { set({ target, openDialog: "destroy" }); },
    openConfirmAction: (target, action, label) => {
      set({ target, openDialog: "confirm-action", confirmAction: action, confirmActionLabel: label });
    },
    closeDialog: () => { set({ openDialog: null, confirmAction: null, confirmActionLabel: null }); },
  }),
);
