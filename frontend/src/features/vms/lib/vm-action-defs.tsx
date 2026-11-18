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
import { useCallback } from "react";
import type { VMAction, ResourceKind } from "../types/vm";
import { canResume, isHaltedState } from "@/lib/guest-state";
import { usePermissions } from "@/hooks/usePermissions";

export interface ActionConfig {
  action: VMAction;
  label: string;
  /** The label as a verb, where a question needs one ("Shut down linux01?"). */
  verb?: string;
  icon: React.ReactNode;
  variant: "outline" | "destructive";
  needsConfirm: boolean;
  /** `state` is guestState(): the halted QEMU state of a running VM, else its status. */
  showWhen: (state: string, kind: ResourceKind) => boolean;
}

/** What a confirmation asks with: the action's verb, or its label. */
export function actionVerb(config: Pick<ActionConfig, "label" | "verb">) {
  return config.verb ?? config.label;
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

/** Also the Snapshots tab's rollback and delete, and a VM's CD-ROM (Media). */
export type GuestAction =
  VMAction | ManagementAction | "rollback" | "delete-snapshot" | "media";

/**
 * The permission an action needs, as its route declares it: the power actions
 * (the status route), a snapshot, a rollback and a VM's media take execute on the
 * guest's kind; a clone,
 * a template and a deploy take manage; a destroy or a snapshot's delete takes
 * delete; a migration is a
 * migration job, which takes manage:migration. Every surface that offers a
 * guest action asks this, so it offers only what the server would accept.
 */
export function guestActionPermission(
  action: GuestAction,
  kind: ResourceKind,
): readonly [action: string, resource: string] {
  const resource = kind === "ct" ? "container" : "vm";
  switch (action) {
    case "clone":
    case "clone-to-template":
    case "convert-to-template":
    case "deploy":
      return ["manage", resource];
    case "migrate":
      return ["manage", "migration"];
    case "destroy":
    case "delete-snapshot":
      return ["delete", resource];
    default:
      return ["execute", resource];
  }
}

/** Whether the signed-in user may run an action on a guest of this kind. */
export function useGuestActionAllowed(): (
  action: GuestAction,
  kind: ResourceKind,
) => boolean {
  const { hasPermission } = usePermissions();
  return useCallback(
    (action, kind) => hasPermission(...guestActionPermission(action, kind)),
    [hasPermission],
  );
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
    verb: "Shut down",
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
    label: "Take snapshot",
    icon: <Camera className="h-4 w-4" />,
    variant: "outline",
    showWhen: (_s, _k, template) => !template,
  },
  {
    action: "deploy",
    label: "Deploy from template",
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
    label: "Clone to template",
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
    label: "Convert to template",
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
