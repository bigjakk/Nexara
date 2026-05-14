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
  template?: boolean;
}

type OpenDialog = "clone" | "clone-to-template" | "deploy" | "migrate" | "destroy" | "convert-to-template" | "confirm-action" | "move-to-folder" | null;

interface VMContextMenuState {
  target: VMContextTarget | null;
  openDialog: OpenDialog;
  confirmAction: string | null;
  confirmActionLabel: string | null;
}

interface VMContextMenuActions {
  openClone: (target: VMContextTarget) => void;
  openCloneToTemplate: (target: VMContextTarget) => void;
  openDeploy: (target: VMContextTarget) => void;
  openMigrate: (target: VMContextTarget) => void;
  openDestroy: (target: VMContextTarget) => void;
  openConvertToTemplate: (target: VMContextTarget) => void;
  openConfirmAction: (target: VMContextTarget, action: string, label: string) => void;
  openMoveToFolder: (target: VMContextTarget) => void;
  closeDialog: () => void;
}

export const useVMContextMenuStore = create<VMContextMenuState & VMContextMenuActions>()(
  (set) => ({
    target: null,
    openDialog: null,
    confirmAction: null,
    confirmActionLabel: null,

    openClone: (target) => { set({ target, openDialog: "clone" }); },
    openCloneToTemplate: (target) => { set({ target, openDialog: "clone-to-template" }); },
    openDeploy: (target) => { set({ target, openDialog: "deploy" }); },
    openMigrate: (target) => { set({ target, openDialog: "migrate" }); },
    openDestroy: (target) => { set({ target, openDialog: "destroy" }); },
    openConvertToTemplate: (target) => { set({ target, openDialog: "convert-to-template" }); },
    openConfirmAction: (target, action, label) => {
      set({ target, openDialog: "confirm-action", confirmAction: action, confirmActionLabel: label });
    },
    openMoveToFolder: (target) => { set({ target, openDialog: "move-to-folder" }); },
    closeDialog: () => { set({ openDialog: null, confirmAction: null, confirmActionLabel: null }); },
  }),
);
