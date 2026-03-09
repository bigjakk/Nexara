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
  FileBox,
  Rocket,
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

export type ManagementAction = "clone" | "clone-to-template" | "deploy" | "migrate" | "convert-to-template" | "destroy";

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
