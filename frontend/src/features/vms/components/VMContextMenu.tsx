import type { ReactNode } from "react";
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuLabel,
  ContextMenuTrigger,
} from "@/components/ui/context-menu";
import { useVMAction } from "../api/vm-queries";
import { lifecycleActions, managementActions } from "../lib/vm-action-defs";
import {
  useVMContextMenuStore,
  type VMContextTarget,
} from "@/stores/vm-context-menu-store";
import { useTaskLogStore } from "@/stores/task-log-store";
import type { VMAction } from "../types/vm";

interface VMContextMenuProps {
  target: VMContextTarget;
  children: ReactNode;
}

export function VMContextMenu({ target, children }: VMContextMenuProps) {
  const { openClone, openMigrate, openDestroy, openConfirmAction } =
    useVMContextMenuStore();
  const setPanelOpen = useTaskLogStore((s) => s.setPanelOpen);
  const setFocusedTask = useTaskLogStore((s) => s.setFocusedTask);
  const actionMutation = useVMAction();

  const normalizedStatus = target.status.toLowerCase();

  const visibleLifecycle = lifecycleActions.filter((a) =>
    a.showWhen(normalizedStatus, target.kind),
  );

  const visibleManagement = managementActions.filter((a) =>
    a.showWhen(normalizedStatus, target.kind),
  );

  function handleLifecycleAction(action: VMAction, needsConfirm: boolean, label: string) {
    if (needsConfirm) {
      openConfirmAction(target, action, label);
    } else {
      actionMutation.mutate(
        {
          clusterId: target.clusterId,
          resourceId: target.resourceId,
          kind: target.kind,
          action,
        },
        {
          onSuccess: (data) => {
            setFocusedTask({
              clusterId: target.clusterId,
              upid: data.upid,
              description: `${label} ${target.name}`,
            });
            setPanelOpen(true);
          },
        },
      );
    }
  }

  function handleManagementAction(action: "clone" | "migrate" | "destroy") {
    if (action === "clone") openClone(target);
    if (action === "migrate") openMigrate(target);
    if (action === "destroy") openDestroy(target);
  }

  return (
    <ContextMenu>
      <ContextMenuTrigger asChild>
        {children}
      </ContextMenuTrigger>
      <ContextMenuContent className="w-44">
        <ContextMenuLabel className="text-xs text-muted-foreground">
          {String(target.vmid)} {target.name}
        </ContextMenuLabel>

        {visibleLifecycle.map((config) => (
          <ContextMenuItem
            key={config.action}
            onClick={() => { handleLifecycleAction(config.action, config.needsConfirm, config.label); }}
          >
            <span className="mr-2">{config.icon}</span>
            {config.label}
          </ContextMenuItem>
        ))}

        {visibleManagement.length > 0 && <ContextMenuSeparator />}

        {visibleManagement.map((config) => (
          <ContextMenuItem
            key={config.action}
            onClick={() => { handleManagementAction(config.action); }}
            className={config.variant === "destructive" ? "text-destructive focus:text-destructive" : ""}
          >
            <span className="mr-2">{config.icon}</span>
            {config.label}
          </ContextMenuItem>
        ))}
      </ContextMenuContent>
    </ContextMenu>
  );
}
