import { useState } from "react";
import { createPortal } from "react-dom";
import {
  Camera,
  ChevronDown,
  Copy,
  ArrowRightLeft,
  MoreHorizontal,
  Trash2,
  FileBox,
  Rocket,
} from "lucide-react";
import { useQueryClient } from "@tanstack/react-query";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { useVMAction } from "../api/vm-queries";
import { TaskProgressBanner } from "./TaskProgressBanner";
import {
  actionVerb,
  lifecycleActions,
  type ActionConfig,
} from "../lib/vm-action-defs";
import type { VMAction, ResourceKind } from "../types/vm";
import { cn } from "@/lib/utils";
import { guestState } from "@/lib/guest-state";

/** Map a completed lifecycle action to the expected VM status. */
function expectedStatus(action: VMAction): string | null {
  switch (action) {
    case "start":
    case "reboot":
    case "resume":
      return "running";
    case "stop":
    case "shutdown":
      return "stopped";
    case "suspend":
      return "suspended";
    default:
      return null;
  }
}

interface VMActionsProps {
  clusterId: string;
  resourceId: string;
  kind: ResourceKind;
  status: string;
  /** A running VM's halted QEMU state (VMResponse.run_state). */
  runState?: string | undefined;
  name: string;
  template?: boolean;
  onSnapshot?: () => void;
  onClone: () => void;
  onCloneToTemplate?: () => void;
  onDeploy?: () => void;
  onMigrate: () => void;
  onDestroy: () => void;
  onConvertToTemplate?: () => void;
  /** Shown first in the row: the page's primary action (its console button). */
  children?: React.ReactNode;
  /**
   * Where the running task's banner goes. Without it the banner sits under
   * the buttons; a page that puts the buttons in its header gives it a
   * full-width place of its own.
   */
  bannerContainer?: HTMLElement | null | undefined;
}

export function VMActions({
  clusterId,
  resourceId,
  kind,
  status,
  runState,
  name,
  template,
  onSnapshot,
  onClone,
  onCloneToTemplate,
  onDeploy,
  onMigrate,
  onDestroy,
  onConvertToTemplate,
  children,
  bannerContainer,
}: VMActionsProps) {
  const queryClient = useQueryClient();
  const actionMutation = useVMAction();
  const [confirmAction, setConfirmAction] = useState<ActionConfig | null>(null);
  const [activeUpid, setActiveUpid] = useState<string | null>(null);
  const [lastAction, setLastAction] = useState<VMAction | null>(null);

  const normalizedStatus = status.toLowerCase();
  const isPending = actionMutation.isPending || activeUpid !== null;

  function dispatchAction(action: VMAction) {
    setLastAction(action);
    actionMutation.mutate(
      { clusterId, resourceId, kind, action },
      {
        onSuccess: (data) => {
          setActiveUpid(data.upid);
          setConfirmAction(null);
        },
      },
    );
  }

  function handleTaskComplete() {
    // Optimistically update the cached VM data with the expected status
    // so the page reflects the new state immediately (the collector will
    // sync the DB on its next cycle).
    if (lastAction) {
      const newStatus = expectedStatus(lastAction);
      if (newStatus) {
        const queryKey = [
          "clusters",
          clusterId,
          kind === "ct" ? "containers" : "vms",
          resourceId,
        ];
        queryClient.setQueryData(
          queryKey,
          (old: Record<string, unknown> | undefined) => {
            if (!old) return old;
            // A finished power task leaves no halted QEMU state, as the
            // server's own write after it (UpdateVMStatus) says.
            return { ...old, status: newStatus, run_state: "" };
          },
        );
      }
    }
    setActiveUpid(null);
    setLastAction(null);
  }

  function handleClick(config: ActionConfig) {
    if (config.needsConfirm) {
      setConfirmAction(config);
    } else {
      dispatchAction(config.action);
    }
  }

  // The actions are ranked rather than laid out ten abreast: the power action
  // that fits the guest's state is a button, its alternatives fold in beside
  // it, and what is rarely wanted or cannot be undone sits behind "More".
  // A halted VM is offered what Proxmox does for its QEMU state.
  const powerActions = lifecycleActions.filter((a) =>
    a.showWhen(guestState(status, runState), kind),
  );
  // Where resuming is offered it leads: it is what the guest waits for.
  const primaryPower =
    powerActions.find((a) => a.action === "resume") ?? powerActions[0];
  const otherPower = powerActions.filter((a) => a !== primaryPower);
  const canConvert =
    !template && normalizedStatus === "stopped" && onConvertToTemplate;

  const banner = (
    <TaskProgressBanner
      clusterId={clusterId}
      upid={activeUpid}
      onComplete={handleTaskComplete}
      description={`${lastAction ?? "action"} ${name}`}
    />
  );

  return (
    <div className="space-y-2">
      <div className="flex flex-wrap items-center gap-2">
        {children}

        {primaryPower && (
          <div className="flex">
            <Button
              variant="outline"
              size="sm"
              className={cn(
                "gap-1.5",
                otherPower.length > 0 && "rounded-r-none",
              )}
              disabled={isPending}
              onClick={() => {
                handleClick(primaryPower);
              }}
            >
              {primaryPower.icon}
              {primaryPower.label}
            </Button>
            {otherPower.length > 0 && (
              <DropdownMenu>
                <DropdownMenuTrigger asChild>
                  <Button
                    variant="outline"
                    size="sm"
                    className="rounded-l-none border-l-0 px-2"
                    aria-label="More power actions"
                    disabled={isPending}
                  >
                    <ChevronDown />
                  </Button>
                </DropdownMenuTrigger>
                <DropdownMenuContent align="start">
                  {otherPower.map((config) => (
                    <DropdownMenuItem
                      key={config.action}
                      className="gap-2"
                      onClick={() => {
                        handleClick(config);
                      }}
                    >
                      {config.icon}
                      {config.label}
                    </DropdownMenuItem>
                  ))}
                </DropdownMenuContent>
              </DropdownMenu>
            )}
          </div>
        )}

        {!template && onSnapshot && (
          <Button
            variant="outline"
            size="sm"
            className="gap-1.5"
            disabled={isPending}
            onClick={onSnapshot}
          >
            <Camera className="h-4 w-4" />
            Snapshot
          </Button>
        )}

        {template && onDeploy && (
          <Button
            variant="outline"
            size="sm"
            className="gap-1.5"
            disabled={isPending}
            onClick={onDeploy}
          >
            <Rocket className="h-4 w-4" />
            Deploy from template
          </Button>
        )}

        <Button
          variant="outline"
          size="sm"
          className="gap-1.5"
          disabled={isPending}
          onClick={onMigrate}
        >
          <ArrowRightLeft className="h-4 w-4" />
          Migrate
        </Button>

        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button
              variant="outline"
              size="sm"
              className="px-2"
              aria-label="More actions"
              disabled={isPending}
            >
              <MoreHorizontal />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuItem className="gap-2" onClick={onClone}>
              <Copy className="h-4 w-4" />
              Clone
            </DropdownMenuItem>
            {!template && onCloneToTemplate && (
              <DropdownMenuItem className="gap-2" onClick={onCloneToTemplate}>
                <FileBox className="h-4 w-4" />
                Clone to template
              </DropdownMenuItem>
            )}
            {canConvert && (
              <DropdownMenuItem className="gap-2" onClick={onConvertToTemplate}>
                <FileBox className="h-4 w-4" />
                Convert to template
              </DropdownMenuItem>
            )}
            <DropdownMenuSeparator />
            {/* A running guest cannot be destroyed; the item says so by being
                unavailable rather than by vanishing. */}
            <DropdownMenuItem
              className="gap-2 text-destructive focus:text-destructive"
              disabled={normalizedStatus === "running"}
              onClick={onDestroy}
            >
              <Trash2 className="h-4 w-4" />
              Destroy
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>

      {bannerContainer != null ? createPortal(banner, bannerContainer) : banner}

      {/* Confirmation dialog */}
      <Dialog
        open={confirmAction !== null}
        onOpenChange={(open) => {
          if (!open) setConfirmAction(null);
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              {confirmAction && actionVerb(confirmAction)} {name}?
            </DialogTitle>
            <DialogDescription>
              Are you sure you want to{" "}
              {confirmAction && actionVerb(confirmAction).toLowerCase()}{" "}
              <strong>{name}</strong>? This action may cause data loss.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button
              variant="outline"
              onClick={() => {
                setConfirmAction(null);
              }}
            >
              Cancel
            </Button>
            <Button
              variant="destructive"
              disabled={actionMutation.isPending}
              onClick={() => {
                if (confirmAction) dispatchAction(confirmAction.action);
              }}
            >
              {confirmAction && actionVerb(confirmAction)}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
