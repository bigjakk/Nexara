import { useState } from "react";
import { Copy, ArrowRightLeft, Trash2 } from "lucide-react";
import { useQueryClient } from "@tanstack/react-query";
import { Button } from "@/components/ui/button";
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
import { lifecycleActions, type ActionConfig } from "../lib/vm-action-defs";
import type { VMAction, ResourceKind } from "../types/vm";

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
  name: string;
  onClone: () => void;
  onMigrate: () => void;
  onDestroy: () => void;
}


export function VMActions({
  clusterId,
  resourceId,
  kind,
  status,
  name,
  onClone,
  onMigrate,
  onDestroy,
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
        queryClient.setQueryData(queryKey, (old: Record<string, unknown> | undefined) => {
          if (!old) return old;
          return { ...old, status: newStatus };
        });
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

  const visibleActions = lifecycleActions.filter((a) =>
    a.showWhen(normalizedStatus, kind),
  );

  return (
    <div className="space-y-2">
      <div className="flex flex-wrap items-center gap-2">
        {visibleActions.map((config) => (
          <Button
            key={config.action}
            variant={config.variant}
            size="sm"
            className="gap-1.5"
            disabled={isPending}
            onClick={() => { handleClick(config); }}
          >
            {config.icon}
            {config.label}
          </Button>
        ))}

        <div className="h-6 w-px bg-border" />

        <Button
          variant="outline"
          size="sm"
          className="gap-1.5"
          disabled={isPending}
          onClick={onClone}
        >
          <Copy className="h-4 w-4" />
          Clone
        </Button>

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

        <Button
          variant="outline"
          size="sm"
          className="gap-1.5 text-destructive"
          disabled={isPending || normalizedStatus === "running"}
          onClick={onDestroy}
        >
          <Trash2 className="h-4 w-4" />
          Destroy
        </Button>
      </div>

      <TaskProgressBanner
        clusterId={clusterId}
        upid={activeUpid}
        kind={kind}
        resourceId={resourceId}
        onComplete={handleTaskComplete}
        description={`${lastAction ?? "action"} ${name}`}
      />

      {/* Confirmation dialog */}
      <Dialog
        open={confirmAction !== null}
        onOpenChange={(open) => { if (!open) setConfirmAction(null); }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Confirm {confirmAction?.label}</DialogTitle>
            <DialogDescription>
              Are you sure you want to {confirmAction?.label.toLowerCase()}{" "}
              <strong>{name}</strong>? This action may cause data loss.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button
              variant="outline"
              onClick={() => { setConfirmAction(null); }}
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
              {confirmAction?.label}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
