import { Fragment, type ReactNode } from "react";
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuLabel,
  ContextMenuTrigger,
} from "@/components/ui/context-menu";
import { Monitor, FolderInput } from "lucide-react";
import { useVMAction } from "../api/vm-queries";
import { guestState } from "@/lib/guest-state";
import {
  actionVerb,
  lifecycleActions,
  managementActions,
  useGuestActionAllowed,
  type ManagementAction,
} from "../lib/vm-action-defs";
import {
  useVMContextMenuStore,
  type VMContextTarget,
} from "@/stores/vm-context-menu-store";
import { useTaskLogStore } from "@/stores/task-log-store";
import { openConsole } from "@/features/console/lib/open-console";
import type { ConsoleClickKeys } from "@/features/console/lib/console-session";
import { usePermissions } from "@/hooks/usePermissions";
import { FavoriteContextItem } from "@/features/favorites/components/FavoriteMenuItem";
import type { VMAction } from "../types/vm";

// Where each management action sits in the menu. One that is in none of these
// lists is appended to the rarer group rather than dropped.
const EVERYDAY: ManagementAction[] = ["snapshot", "deploy", "migrate"];
const RARER: ManagementAction[] = [
  "clone",
  "clone-to-template",
  "convert-to-template",
];
const LAST: ManagementAction[] = ["destroy"];
const PLACED = [...EVERYDAY, ...RARER, ...LAST];

interface VMContextMenuProps {
  target: VMContextTarget;
  children: ReactNode;
  /** Called when any menu action is chosen — lets hosts (e.g. the command
   * palette) close themselves before the action's dialog opens. */
  onAction?: (() => void) | undefined;
}

export function VMContextMenu({
  target,
  children,
  onAction,
}: VMContextMenuProps) {
  const {
    openSnapshot,
    openClone,
    openCloneToTemplate,
    openDeploy,
    openMigrate,
    openDestroy,
    openConvertToTemplate,
    openConfirmAction,
    openMoveToFolder,
  } = useVMContextMenuStore();
  const setPanelOpen = useTaskLogStore((s) => s.setPanelOpen);
  const setFocusedTask = useTaskLogStore((s) => s.setFocusedTask);
  const actionMutation = useVMAction();
  const { canConsole, hasPermission } = usePermissions();

  const normalizedStatus = target.status.toLowerCase();
  const consoleAllowed = canConsole(target.kind === "ct" ? "container" : "vm");

  const allowed = useGuestActionAllowed();
  const visibleLifecycle = lifecycleActions.filter(
    (a) =>
      a.showWhen(guestState(target.status, target.runState), target.kind) &&
      allowed(a.action, target.kind),
  );

  const visibleManagement = managementActions.filter(
    (a) =>
      a.showWhen(normalizedStatus, target.kind, target.template) &&
      allowed(a.action, target.kind),
  );

  function handleLifecycleAction(
    action: VMAction,
    needsConfirm: boolean,
    label: string,
  ) {
    onAction?.();
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

  function handleManagementAction(action: ManagementAction) {
    onAction?.();
    if (action === "snapshot") openSnapshot(target);
    if (action === "clone") openClone(target);
    if (action === "clone-to-template") openCloneToTemplate(target);
    if (action === "deploy") openDeploy(target);
    if (action === "migrate") openMigrate(target);
    if (action === "convert-to-template") openConvertToTemplate(target);
    if (action === "destroy") openDestroy(target);
  }

  // onClick rather than onSelect: Radix hands onSelect an event without the
  // click's modifier keys.
  function handleOpenConsole(keys: ConsoleClickKeys) {
    onAction?.();
    openConsole(
      {
        clusterID: target.clusterId,
        node: target.currentNode,
        vmid: target.vmid,
        type: target.kind === "ct" ? "ct_vnc" : "vm_vnc",
        resourceId: target.resourceId,
        kind: target.kind,
        guestName: target.name,
      },
      keys,
    );
  }

  const managementItem = (action: ManagementAction) => {
    const config = visibleManagement.find((a) => a.action === action);
    if (!config) return null;
    return (
      <ContextMenuItem
        key={config.action}
        onClick={() => {
          handleManagementAction(config.action);
        }}
        className={
          config.variant === "destructive"
            ? "text-destructive focus:text-destructive"
            : ""
        }
      >
        <span className="mr-2">{config.icon}</span>
        {config.label}
      </ContextMenuItem>
    );
  };

  // The same order as the guest page's actions: the console, then power, then
  // what is done to a guest most days, then the rarer ones, and Destroy last
  // behind a rule of its own. An empty group takes its separator with it.
  const groups = [
    normalizedStatus === "running" && consoleAllowed
      ? [
          <ContextMenuItem key="console" onClick={handleOpenConsole}>
            <span className="mr-2">
              <Monitor className="h-4 w-4" />
            </span>
            Console
          </ContextMenuItem>,
        ]
      : [],
    visibleLifecycle.map((config) => (
      <ContextMenuItem
        key={config.action}
        onClick={() => {
          handleLifecycleAction(
            config.action,
            config.needsConfirm,
            actionVerb(config),
          );
        }}
      >
        <span className="mr-2">{config.icon}</span>
        {config.label}
      </ContextMenuItem>
    )),
    EVERYDAY.map(managementItem),
    [
      ...RARER.map(managementItem),
      // Filing a guest takes manage:vm_folder.
      ...(hasPermission("manage", "vm_folder")
        ? [
            <ContextMenuItem
              key="move-to-folder"
              onClick={() => {
                onAction?.();
                openMoveToFolder(target);
              }}
            >
              <span className="mr-2">
                <FolderInput className="h-4 w-4" />
              </span>
              Move to folder…
            </ContextMenuItem>,
          ]
        : []),
      <FavoriteContextItem
        key="favorite"
        target={{
          resource_type: "vm",
          cluster_id: target.clusterId,
          ref: String(target.vmid),
        }}
        onAction={onAction}
      />,
      // An action nobody has given a place yet still shows, here.
      ...visibleManagement
        .filter((a) => !PLACED.includes(a.action))
        .map((a) => managementItem(a.action)),
    ],
    LAST.map(managementItem),
  ]
    .map((items) => items.filter((item) => item !== null))
    .filter((items) => items.length > 0);

  return (
    <ContextMenu modal={false}>
      <ContextMenuTrigger asChild>{children}</ContextMenuTrigger>
      <ContextMenuContent className="w-52">
        <ContextMenuLabel className="flex items-baseline gap-2 font-normal">
          <span className="min-w-0 truncate text-[13px] font-semibold">
            {target.name}
          </span>
          <span className="shrink-0 text-xs text-muted-foreground">
            {target.kind === "ct" ? "CT" : "VM"} {String(target.vmid)}
          </span>
        </ContextMenuLabel>
        {groups.map((items, i) => (
          <Fragment key={i}>
            <ContextMenuSeparator />
            {items}
          </Fragment>
        ))}
      </ContextMenuContent>
    </ContextMenu>
  );
}
