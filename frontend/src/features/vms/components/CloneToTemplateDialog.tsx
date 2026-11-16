import { useEffect, useMemo, useState } from "react";
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
import { CheckField, Field, NativeSelect } from "@/components/page/Field";
import {
  useClusterNodes,
  useClusterStorage,
} from "@/features/clusters/api/cluster-queries";
import { useCloneToTemplate, useClusterVMIDs } from "../api/vm-queries";
import { useSaveOutcome } from "@/hooks/useSaveOutcome";
import { TaskProgressBanner } from "./TaskProgressBanner";
import type { ResourceKind } from "../types/vm";

interface CloneToTemplateDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  clusterId: string;
  resourceId: string;
  kind: ResourceKind;
  sourceName: string;
}

export function CloneToTemplateDialog({
  open,
  onOpenChange,
  clusterId,
  resourceId,
  kind,
  sourceName,
}: CloneToTemplateDialogProps) {
  const { data: nodes } = useClusterNodes(clusterId);
  const { data: storageList } = useClusterStorage(clusterId);
  const { data: usedVMIDs } = useClusterVMIDs(clusterId);
  const cloneToTemplateMutation = useCloneToTemplate();
  const settle = useSaveOutcome(open ? resourceId : null);

  const storageOptions = storageList
    ? [
        ...new Set(
          storageList
            .filter(
              (s) => s.active && s.enabled && s.content.includes("images"),
            )
            .map((s) => s.storage),
        ),
      ].sort()
    : [];

  const nextAvailableId = useMemo(() => {
    if (!usedVMIDs || usedVMIDs.size === 0) return 100;
    let candidate = 100;
    while (usedVMIDs.has(candidate)) candidate++;
    return candidate;
  }, [usedVMIDs]);

  const [upid, setUpid] = useState<string | null>(null);
  const [newId, setNewId] = useState("");
  const [name, setName] = useState("");
  const [target, setTarget] = useState("");
  const [full, setFull] = useState(true);
  const [storage, setStorage] = useState("");

  useEffect(() => {
    if (open && newId === "" && usedVMIDs) {
      setNewId(String(nextAvailableId));
    }
    if (open && name === "") {
      setName(`${sourceName}-template`);
    }
  }, [open, usedVMIDs, nextAvailableId, newId, name, sourceName]);

  const numericId = Number(newId);
  const isDuplicate = usedVMIDs ? usedVMIDs.has(numericId) : false;
  const isValid = newId.length > 0 && numericId > 0;

  function handleSubmit(e: React.SyntheticEvent) {
    e.preventDefault();
    settle(
      cloneToTemplateMutation.mutateAsync({
        clusterId,
        resourceId,
        kind,
        body: {
          new_id: Number(newId),
          name,
          target,
          full,
          storage: full ? storage : "",
        },
      }),
      {
        action: `Cloning ${sourceName} to a template`,
        onSuccess: (data) => {
          setUpid(data.upid);
        },
        onError: () => undefined, // shown below from the mutation's state
      },
    );
  }

  function handleClose() {
    setUpid(null);
    setNewId("");
    setName("");
    setTarget("");
    setFull(true);
    setStorage("");
    cloneToTemplateMutation.reset();
    onOpenChange(false);
  }

  const typeLabel = kind === "ct" ? "container" : "VM";

  return (
    <Dialog open={open} onOpenChange={handleClose}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Clone to template</DialogTitle>
          <DialogDescription>
            Clones <strong>{sourceName}</strong> and turns the copy into a
            read-only template. The {typeLabel} itself is not changed.
          </DialogDescription>
        </DialogHeader>

        {upid ? (
          <div className="space-y-3">
            <TaskProgressBanner
              clusterId={clusterId}
              upid={upid}
              onComplete={() => {
                handleClose();
              }}
              description={`Clone ${sourceName} to template`}
            />
            <p className="text-[12.5px] text-muted-foreground">
              When the clone is done it is converted to a template
              automatically.
            </p>
          </div>
        ) : (
          <form onSubmit={handleSubmit} className="space-y-4">
            <Field
              label="New VMID"
              htmlFor="ctt-new-id"
              warning={
                isDuplicate && `VMID ${String(numericId)} may already be in use`
              }
            >
              <Input
                id="ctt-new-id"
                type="number"
                min={1}
                value={newId}
                onChange={(e) => {
                  setNewId(e.target.value);
                }}
                placeholder="e.g. 200"
                required
                className="max-w-[10rem]"
              />
            </Field>
            <Field label="Template name" htmlFor="ctt-name">
              <Input
                id="ctt-name"
                value={name}
                onChange={(e) => {
                  setName(e.target.value);
                }}
                placeholder="Optional"
              />
            </Field>
            <Field label="Target node" htmlFor="ctt-target">
              <NativeSelect
                id="ctt-target"
                value={target}
                onChange={(e) => {
                  setTarget(e.target.value);
                }}
              >
                <option value="">Same node</option>
                {nodes?.map((n) => (
                  <option key={n.id} value={n.name}>
                    {n.name}
                  </option>
                ))}
              </NativeSelect>
            </Field>
            <CheckField
              id="ctt-full"
              label="Full clone"
              hint="An independent copy. Unticked, the clone stays linked to its source."
              checked={full}
              onCheckedChange={setFull}
            />
            {full && (
              <Field label="Storage" htmlFor="ctt-storage">
                <NativeSelect
                  id="ctt-storage"
                  value={storage}
                  onChange={(e) => {
                    setStorage(e.target.value);
                  }}
                >
                  <option value="">Same as source</option>
                  {storageOptions.map((s) => (
                    <option key={s} value={s}>
                      {s}
                    </option>
                  ))}
                </NativeSelect>
              </Field>
            )}

            {cloneToTemplateMutation.isError && (
              <p className="text-sm text-destructive">
                {cloneToTemplateMutation.error.message}
              </p>
            )}

            <DialogFooter className="gap-2 sm:gap-0">
              <Button type="button" variant="outline" onClick={handleClose}>
                Cancel
              </Button>
              <Button
                type="submit"
                disabled={!isValid || cloneToTemplateMutation.isPending}
              >
                {cloneToTemplateMutation.isPending
                  ? "Cloning..."
                  : "Clone to template"}
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  );
}
