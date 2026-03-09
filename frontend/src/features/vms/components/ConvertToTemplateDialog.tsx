import { useState } from "react";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { AlertTriangle } from "lucide-react";
import { useConvertToTemplate } from "../api/vm-queries";
import { TaskProgressBanner } from "./TaskProgressBanner";
import type { ResourceKind } from "../types/vm";

interface ConvertToTemplateDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  clusterId: string;
  resourceId: string;
  kind: ResourceKind;
  resourceName: string;
}

export function ConvertToTemplateDialog({
  open,
  onOpenChange,
  clusterId,
  resourceId,
  kind,
  resourceName,
}: ConvertToTemplateDialogProps) {
  const convertMutation = useConvertToTemplate();
  const [upid, setUpid] = useState<string | null>(null);
  const [confirmText, setConfirmText] = useState("");

  const isConfirmed = confirmText === resourceName;
  const typeLabel = kind === "ct" ? "container" : "VM";

  function handleSubmit(e: React.SyntheticEvent) {
    e.preventDefault();
    convertMutation.mutate(
      { clusterId, resourceId, kind },
      {
        onSuccess: (data) => {
          setUpid(data.upid);
        },
      },
    );
  }

  function handleClose() {
    setUpid(null);
    setConfirmText("");
    convertMutation.reset();
    onOpenChange(false);
  }

  return (
    <Dialog open={open} onOpenChange={handleClose}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Convert to Template</DialogTitle>
          <DialogDescription>
            Convert <strong>{resourceName}</strong> to a template.
          </DialogDescription>
        </DialogHeader>

        {upid ? (
          <div className="space-y-4">
            <TaskProgressBanner
              clusterId={clusterId}
              upid={upid}
              onComplete={() => { handleClose(); }}
              description={`Convert ${resourceName} to template`}
            />
          </div>
        ) : (
          <form onSubmit={handleSubmit} className="space-y-4">
            <div className="rounded-md border border-yellow-500/50 bg-yellow-500/10 p-3">
              <div className="flex items-start gap-2">
                <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-yellow-600 dark:text-yellow-400" />
                <div className="text-sm text-yellow-700 dark:text-yellow-300">
                  <p className="font-medium">This action is irreversible</p>
                  <p className="mt-1">
                    Converting this {typeLabel} to a template will make it read-only.
                    It can no longer be started or modified — only cloned to create new {typeLabel}s.
                    To get a runnable {typeLabel} back, you must clone the template.
                  </p>
                </div>
              </div>
            </div>

            <div className="space-y-2">
              <Label htmlFor="confirm-name">
                Type <strong>{resourceName}</strong> to confirm
              </Label>
              <Input
                id="confirm-name"
                value={confirmText}
                onChange={(e) => { setConfirmText(e.target.value); }}
                placeholder={resourceName}
                autoComplete="off"
              />
            </div>

            {convertMutation.isError && (
              <p className="text-sm text-destructive">
                {convertMutation.error.message}
              </p>
            )}

            <DialogFooter>
              <Button type="button" variant="outline" onClick={handleClose}>
                Cancel
              </Button>
              <Button
                type="submit"
                variant="destructive"
                disabled={!isConfirmed || convertMutation.isPending}
              >
                {convertMutation.isPending ? "Converting..." : "Convert to Template"}
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  );
}
