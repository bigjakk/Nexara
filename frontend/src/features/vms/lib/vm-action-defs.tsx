import {
  Play,
  Square,
  Power,
  RotateCcw,
  Zap,
  Pause,
  PlayCircle,
  Copy,
  ArrowRightLeft,
  Trash2,
} from "lucide-react";
import type { VMAction, ResourceKind } from "../types/vm";

export interface ActionConfig {
  action: VMAction;
  label: string;
  icon: React.ReactNode;
  variant: "outline" | "destructive";
  needsConfirm: boolean;
  showWhen: (status: string, kind: ResourceKind) => boolean;
}

export type ManagementAction = "clone" | "migrate" | "destroy";

export interface ManagementActionConfig {
  action: ManagementAction;
  label: string;
  icon: React.ReactNode;
  variant: "outline" | "destructive";
  showWhen: (status: string, kind: ResourceKind) => boolean;
}

export const lifecycleActions: ActionConfig[] = [
  {
    action: "start",
    label: "Start",
    icon: <Play className="h-4 w-4" />,
    variant: "outline",
    needsConfirm: false,
    showWhen: (s) => s === "stopped" || s === "suspended",
  },
  {
    action: "shutdown",
    label: "Shutdown",
    icon: <Power className="h-4 w-4" />,
    variant: "outline",
    needsConfirm: false,
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
    showWhen: (s) => s === "running",
  },
  {
    action: "reset",
    label: "Reset",
    icon: <Zap className="h-4 w-4" />,
    variant: "outline",
    needsConfirm: true,
    showWhen: (s, k) => s === "running" && k === "vm",
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
    showWhen: (s) => s === "suspended",
  },
];

export const managementActions: ManagementActionConfig[] = [
  {
    action: "clone",
    label: "Clone",
    icon: <Copy className="h-4 w-4" />,
    variant: "outline",
    showWhen: () => true,
  },
  {
    action: "migrate",
    label: "Migrate",
    icon: <ArrowRightLeft className="h-4 w-4" />,
    variant: "outline",
    showWhen: () => true,
  },
  {
    action: "destroy",
    label: "Destroy",
    icon: <Trash2 className="h-4 w-4" />,
    variant: "destructive",
    showWhen: (s) => s !== "running",
  },
];
