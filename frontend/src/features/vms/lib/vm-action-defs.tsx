import {
  Play,
  Square,
  Power,
  RotateCcw,
  Zap,
  Pause,
  PlayCircle,
  Camera,
  Copy,
  ArrowRightLeft,
  Trash2,
  FileBox,
  Rocket,
} from "lucide-react";
import type { VMAction, ResourceKind } from "../types/vm";
import { canResume, isHaltedState } from "@/lib/guest-state";

export interface ActionConfig {
  action: VMAction;
  label: string;
  icon: React.ReactNode;
  variant: "outline" | "destructive";
  needsConfirm: boolean;
  /** `state` is guestState(): the halted QEMU state of a running VM, else its status. */
  showWhen: (state: string, kind: ResourceKind) => boolean;
}

/**
 * A VM whose vCPUs are halted, or paused, can still be stopped or reset
 * (qemu-server's stop and reset check only that the process runs). Shutdown,
 * reboot and start are not offered: Proxmox refuses shutdown and reboot for a
 * paused or sleeping VM (vm_is_paused) and start for any running one, and a
 * halted guest cannot answer the ACPI request anyway.
 */
function haltedVM(s: string, k: ResourceKind): boolean {
  return k === "vm" && (s === "suspended" || isHaltedState(s));
}

export type ManagementAction =
  | "snapshot"
  | "clone"
  | "clone-to-template"
  | "deploy"
  | "migrate"
  | "convert-to-template"
  | "destroy";

export interface ManagementActionConfig {
  action: ManagementAction;
  label: string;
  icon: React.ReactNode;
  variant: "outline" | "destructive";
  showWhen: (status: string, kind: ResourceKind, template?: boolean) => boolean;
}

export const lifecycleActions: ActionConfig[] = [
  {
    action: "start",
    label: "Start",
    icon: <Play className="h-4 w-4" />,
    variant: "outline",
    needsConfirm: false,
    // Not for a suspended container: pct suspend checkpoints it to disk and
    // stops it (lxc-checkpoint -s, vm_suspend in pve-container's
    // src/PVE/API2/LXC/Status.pm), and only resume restores that checkpoint
    // (lxc-checkpoint -r); a start boots it afresh and leaves it behind.
    showWhen: (s) => s === "stopped",
  },
  {
    action: "shutdown",
    label: "Shutdown",
    icon: <Power className="h-4 w-4" />,
    variant: "outline",
    needsConfirm: true,
    showWhen: (s) => s === "running",
  },
  {
    action: "reboot",
    label: "Reboot",
    icon: <RotateCcw className="h-4 w-4" />,
    variant: "outline",
    needsConfirm: false,
    showWhen: (s) => s === "running",
  },
  {
    action: "stop",
    label: "Stop",
    icon: <Square className="h-4 w-4" />,
    variant: "outline",
    needsConfirm: true,
    showWhen: (s, k) => s === "running" || haltedVM(s, k),
  },
  {
    action: "reset",
    label: "Reset",
    icon: <Zap className="h-4 w-4" />,
    variant: "outline",
    needsConfirm: true,
    showWhen: (s, k) => (s === "running" && k === "vm") || haltedVM(s, k),
  },
  {
    action: "suspend",
    label: "Suspend",
    icon: <Pause className="h-4 w-4" />,
    variant: "outline",
    needsConfirm: false,
    showWhen: (s) => s === "running",
  },
  {
    action: "resume",
    label: "Resume",
    icon: <PlayCircle className="h-4 w-4" />,
    variant: "outline",
    needsConfirm: false,
    showWhen: (s) => canResume(s),
  },
];

export const managementActions: ManagementActionConfig[] = [
  {
    action: "snapshot",
    label: "Take Snapshot",
    icon: <Camera className="h-4 w-4" />,
    variant: "outline",
    showWhen: (_s, _k, template) => !template,
  },
  {
    action: "deploy",
    label: "Deploy from Template",
    icon: <Rocket className="h-4 w-4" />,
    variant: "outline",
    showWhen: (_s, _k, template) => template === true,
  },
  {
    action: "clone",
    label: "Clone",
    icon: <Copy className="h-4 w-4" />,
    variant: "outline",
    showWhen: () => true,
  },
  {
    action: "clone-to-template",
    label: "Clone to Template",
    icon: <FileBox className="h-4 w-4" />,
    variant: "outline",
    showWhen: (_s, _k, template) => !template,
  },
  {
    action: "migrate",
    label: "Migrate",
    icon: <ArrowRightLeft className="h-4 w-4" />,
    variant: "outline",
    showWhen: () => true,
  },
  {
    action: "convert-to-template",
    label: "Convert to Template",
    icon: <FileBox className="h-4 w-4" />,
    variant: "outline",
    showWhen: (s, _k, template) => s === "stopped" && !template,
  },
  {
    action: "destroy",
    label: "Destroy",
    icon: <Trash2 className="h-4 w-4" />,
    variant: "destructive",
    showWhen: (s) => s !== "running",
  },
];
