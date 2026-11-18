import { useRef, useState } from "react";
import { ChevronRight, Upload, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { EmptyState } from "@/components/EmptyState";
import { DetailField } from "@/components/DetailField";
import { DefGrid, Panel } from "@/components/page/Panel";
import { cn } from "@/lib/utils";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { useImportJobs, useCancelImport } from "../api/import-queries";
import type { VMImportJob } from "@/types/api";
import { DateTime } from "@/components/DateTime";
import { usePermissions } from "@/hooks/usePermissions";

interface ImportHistoryTableProps {
  clusterId: string;
}

const STATUS_LABEL: Record<string, string> = {
  pending: "Pending",
  running: "Running",
  completed: "Completed",
  failed: "Failed",
  cancelled: "Cancelled",
};

/**
 * An import's state. Only one in flight or one that failed takes a colour:
 * running is blue over a moving bar (Proxmox reports no fraction
 * for an import), failed is red, and a finished one is plain text.
 */
function ImportStatus({ status }: { status: string }) {
  const label = STATUS_LABEL[status] ?? status;
  if (status === "running") {
    return (
      <span className="flex items-center gap-2 font-medium text-info">
        {label}
        <span
          aria-hidden="true"
          className="h-1.5 w-14 overflow-hidden rounded-full bg-foreground/10"
        >
          <span className="block h-full w-[30%] animate-task-indeterminate rounded-full bg-info" />
        </span>
      </span>
    );
  }
  return (
    <span
      className={cn(
        status === "failed"
          ? "font-medium text-red-700 dark:text-red-400"
          : "text-muted-foreground",
      )}
    >
      {label}
    </span>
  );
}

function importName(job: VMImportJob): string {
  return job.name || `VM ${String(job.target_vmid)}`;
}

/** A live import's VM runs while it imports, and stops with its task. */
function stopsItsVm(job: VMImportJob): boolean {
  return Boolean(job.upid) && job.options["live_import"] === true;
}

export function ImportHistoryTable({ clusterId }: ImportHistoryTableProps) {
  const { data: jobs, isLoading } = useImportJobs(clusterId);
  const cancelMutation = useCancelImport();
  const { canManage, canDelete, canExecute } = usePermissions();
  // Cancelling an import takes manage:vm_import; the page itself only view.
  const canCancel = canManage("vm_import");
  // Deleting its VM as well takes delete:vm, as the VM's own delete does, and
  // stopping a live import's VM takes execute:vm, as the power route does.
  const canDeleteVm = canDelete("vm");
  const canStopVm = canExecute("vm");
  const [expanded, setExpanded] = useState<string | null>(null);
  const [cancelTarget, setCancelTarget] = useState<VMImportJob | null>(null);
  const [deleteVm, setDeleteVm] = useState(false);
  // An import whose create has not reached Proxmox (no UPID) has no VM of its
  // own to delete; its VMID may be another guest's.
  const offerDelete = canDeleteVm && Boolean(cancelTarget?.upid);
  // Where focus goes when Cancel import closes: the table, whose X button
  // goes once the job is no longer running.
  const table = useRef<HTMLDivElement>(null);

  if (isLoading) {
    return (
      <Panel title="History" aside="Loading...">
        <div className="space-y-2">
          {Array.from({ length: 4 }).map((_, i) => (
            <Skeleton key={i} className="h-8 w-full" />
          ))}
        </div>
      </Panel>
    );
  }
  if (!jobs || jobs.length === 0) {
    return (
      <EmptyState
        icon={Upload}
        title="No imports yet"
        description="A guest imported from an OVA or OVF appliance, a disk image or an ESXi host is listed here with its outcome."
      />
    );
  }

  const active = jobs.filter(
    (j) => j.status === "pending" || j.status === "running",
  ).length;

  function confirmCancel() {
    if (!cancelTarget) return;
    cancelMutation.mutate(
      { clusterId, id: cancelTarget.id, deleteVm: offerDelete && deleteVm },
      {
        onSettled: () => {
          setCancelTarget(null);
          setDeleteVm(false);
        },
      },
    );
  }

  return (
    <>
      <Panel
        flush
        title="History"
        aside={
          active > 0
            ? `${String(jobs.length)}, ${String(active)} in progress`
            : String(jobs.length)
        }
      >
        <div
          ref={table}
          role="region"
          aria-label="Import history"
          tabIndex={-1}
          className="outline-hidden"
        >
          <Table>
            <TableHeader>
              <TableRow className="hover:bg-transparent">
                <TableHead>Name</TableHead>
                <TableHead>Target</TableHead>
                <TableHead>Source</TableHead>
                <TableHead>Status</TableHead>
                <TableHead>Created</TableHead>
                <TableHead className="w-10" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {jobs.map((job: VMImportJob) => {
                const isOpen = expanded === job.id;
                const cancellable =
                  canCancel &&
                  (job.status === "pending" || job.status === "running") &&
                  (canStopVm || !stopsItsVm(job));
                return (
                  <RowGroup
                    key={job.id}
                    job={job}
                    isOpen={isOpen}
                    cancellable={cancellable}
                    onToggle={() => {
                      setExpanded(isOpen ? null : job.id);
                    }}
                    onCancel={() => {
                      setDeleteVm(false);
                      setCancelTarget(job);
                    }}
                  />
                );
              })}
            </TableBody>
          </Table>
        </div>
      </Panel>

      <Dialog
        open={cancelTarget !== null}
        onOpenChange={(o) => {
          if (!o) {
            setCancelTarget(null);
            setDeleteVm(false);
          }
        }}
      >
        <DialogContent className="max-w-md" fallbackFocus={() => table.current}>
          <DialogHeader>
            <DialogTitle>Cancel import?</DialogTitle>
            <DialogDescription>
              This stops the running import task for{" "}
              {cancelTarget?.name ||
                `VM ${String(cancelTarget?.target_vmid ?? "")}`}
              .
              {cancelTarget &&
                stopsItsVm(cancelTarget) &&
                " It is a live import, so its VM, which runs while it imports, stops too."}
            </DialogDescription>
          </DialogHeader>
          {offerDelete && (
            <label className="flex items-start gap-2 text-sm">
              <Checkbox
                checked={deleteVm}
                onCheckedChange={(v) => {
                  setDeleteVm(v === true);
                }}
              />
              <span>
                Also delete the partially created VM
                {cancelTarget?.target_vmid
                  ? ` (VMID ${String(cancelTarget.target_vmid)})`
                  : ""}
                <span className="block text-xs text-muted-foreground">
                  Leave unchecked to keep the disks created so far for
                  inspection.
                </span>
              </span>
            </label>
          )}
          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              onClick={() => {
                setCancelTarget(null);
                setDeleteVm(false);
              }}
            >
              Keep importing
            </Button>
            <Button
              type="button"
              variant="destructive"
              disabled={cancelMutation.isPending}
              onClick={confirmCancel}
            >
              {cancelMutation.isPending ? "Cancelling…" : "Cancel import"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}

interface RowGroupProps {
  job: VMImportJob;
  isOpen: boolean;
  cancellable: boolean;
  onToggle: () => void;
  onCancel: () => void;
}

function RowGroup({
  job,
  isOpen,
  cancellable,
  onToggle,
  onCancel,
}: RowGroupProps) {
  return (
    <>
      <TableRow className="cursor-pointer" onClick={onToggle}>
        <TableCell className="py-2 font-medium">
          <span className="flex items-center gap-1.5">
            <ChevronRight
              className={cn(
                "h-3.5 w-3.5 shrink-0 text-muted-foreground transition-transform",
                isOpen && "rotate-90",
              )}
            />
            {importName(job)}
          </span>
        </TableCell>
        <TableCell className="py-2 text-muted-foreground">
          {job.target_node} / {job.target_storage}
        </TableCell>
        <TableCell className="py-2 text-muted-foreground">
          {job.source_acquisition}
        </TableCell>
        <TableCell className="py-2">
          <ImportStatus status={job.status} />
        </TableCell>
        <TableCell className="whitespace-nowrap py-2 text-[12.5px] text-muted-foreground">
          <DateTime value={job.created_at} />
        </TableCell>
        <TableCell className="py-1 text-right">
          {cancellable && (
            <Button
              aria-label={`Cancel import of ${importName(job)}`}
              variant="ghost"
              size="sm"
              className="h-7 w-7 p-0"
              onClick={(e) => {
                e.stopPropagation();
                onCancel();
              }}
            >
              <X />
            </Button>
          )}
        </TableCell>
      </TableRow>
      {isOpen && (
        <TableRow className="bg-foreground/[0.03] hover:bg-foreground/[0.03]">
          <TableCell colSpan={6} className="py-3.5 pl-[42px]">
            <DefGrid>
              <DetailField label="VMID" variant="mono">
                {job.target_vmid}
              </DetailField>
              <DetailField label="Format">
                {job.source_format || "—"}
              </DetailField>
              <div className="col-span-full">
                <dt className="text-xs text-muted-foreground">Source</dt>
                <dd className="break-all">{job.source_ref}</dd>
              </div>
              {job.upid && (
                <div className="col-span-full">
                  <dt className="text-xs text-muted-foreground">Task UPID</dt>
                  <dd className="break-all font-mono text-[12.5px]">
                    {job.upid}
                  </dd>
                </div>
              )}
              {job.failure_reason && (
                <div className="col-span-full">
                  <dt className="text-xs text-muted-foreground">Failure</dt>
                  <dd className="text-red-700 dark:text-red-400">
                    {job.failure_reason}
                  </dd>
                </div>
              )}
              {job.warnings.length > 0 && (
                <div className="col-span-full">
                  <dt className="text-xs text-muted-foreground">Warnings</dt>
                  <dd>
                    <ul className="list-inside list-disc">
                      {job.warnings.map((w, i) => (
                        <li key={`${w.type}-${String(i)}`}>
                          {w.type.replace(/-/g, " ")}
                          {w.value ? `: ${w.value}` : ""}
                        </li>
                      ))}
                    </ul>
                  </dd>
                </div>
              )}
            </DefGrid>
          </TableCell>
        </TableRow>
      )}
    </>
  );
}
