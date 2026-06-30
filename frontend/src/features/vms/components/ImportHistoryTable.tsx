import { useState } from "react";
import { ChevronRight, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { useImportJobs, useCancelImport } from "../api/import-queries";
import type { VMImportJob } from "@/types/api";

interface ImportHistoryTableProps {
  clusterId: string;
}

const statusClass: Record<string, string> = {
  pending: "bg-blue-500/10 text-blue-600",
  running: "bg-amber-500/10 text-amber-600",
  completed: "bg-emerald-500/10 text-emerald-600",
  failed: "bg-red-500/10 text-red-600",
  cancelled: "bg-muted text-muted-foreground",
};

function StatusBadge({ status }: { status: string }) {
  return (
    <span className={`rounded px-1.5 py-0.5 text-xs font-medium ${statusClass[status] ?? "bg-muted text-muted-foreground"}`}>
      {status}
    </span>
  );
}

export function ImportHistoryTable({ clusterId }: ImportHistoryTableProps) {
  const { data: jobs, isLoading } = useImportJobs(clusterId);
  const cancelMutation = useCancelImport();
  const [expanded, setExpanded] = useState<string | null>(null);

  if (isLoading) {
    return <p className="text-sm text-muted-foreground">Loading import history…</p>;
  }
  if (!jobs || jobs.length === 0) {
    return <p className="text-sm text-muted-foreground">No imports yet.</p>;
  }

  return (
    <div className="rounded-md border border-border">
      <table className="w-full text-sm">
        <thead>
          <tr className="border-b border-border text-left text-xs text-muted-foreground">
            <th className="w-8" />
            <th className="px-3 py-2">Name</th>
            <th className="px-3 py-2">Target</th>
            <th className="px-3 py-2">Source</th>
            <th className="px-3 py-2">Status</th>
            <th className="px-3 py-2">Created</th>
            <th className="px-3 py-2" />
          </tr>
        </thead>
        <tbody>
          {jobs.map((job: VMImportJob) => {
            const isOpen = expanded === job.id;
            const cancellable = job.status === "pending" || job.status === "running";
            return (
              <RowGroup
                key={job.id}
                job={job}
                isOpen={isOpen}
                cancellable={cancellable}
                onToggle={() => { setExpanded(isOpen ? null : job.id); }}
                onCancel={() => {
                  cancelMutation.mutate({ clusterId, id: job.id, deleteVm: false });
                }}
                cancelPending={cancelMutation.isPending}
              />
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

interface RowGroupProps {
  job: VMImportJob;
  isOpen: boolean;
  cancellable: boolean;
  onToggle: () => void;
  onCancel: () => void;
  cancelPending: boolean;
}

function RowGroup({ job, isOpen, cancellable, onToggle, onCancel, cancelPending }: RowGroupProps) {
  return (
    <>
      <tr className="cursor-pointer border-b border-border last:border-b-0 hover:bg-muted/40" onClick={onToggle}>
        <td className="px-2 py-2">
          <ChevronRight className={`h-4 w-4 transition-transform ${isOpen ? "rotate-90" : ""}`} />
        </td>
        <td className="px-3 py-2">{job.name || `VM ${String(job.target_vmid)}`}</td>
        <td className="px-3 py-2 text-muted-foreground">{job.target_node} / {job.target_storage}</td>
        <td className="px-3 py-2 text-muted-foreground">{job.source_acquisition}</td>
        <td className="px-3 py-2"><StatusBadge status={job.status} /></td>
        <td className="px-3 py-2 text-muted-foreground">{new Date(job.created_at).toLocaleString()}</td>
        <td className="px-3 py-2 text-right">
          {cancellable && (
            <Button
              variant="ghost"
              size="sm"
              disabled={cancelPending}
              onClick={(e) => { e.stopPropagation(); onCancel(); }}
            >
              <X className="h-4 w-4" />
            </Button>
          )}
        </td>
      </tr>
      {isOpen && (
        <tr className="border-b border-border bg-muted/20 last:border-b-0">
          <td />
          <td colSpan={6} className="px-3 py-2">
            <dl className="grid grid-cols-[160px_1fr] gap-x-4 gap-y-1 text-xs">
              <dt className="text-muted-foreground">VMID</dt>
              <dd>{job.target_vmid}</dd>
              <dt className="text-muted-foreground">Source</dt>
              <dd className="break-all">{job.source_ref}</dd>
              <dt className="text-muted-foreground">Format</dt>
              <dd>{job.source_format || "—"}</dd>
              {job.upid && (
                <>
                  <dt className="text-muted-foreground">Task UPID</dt>
                  <dd className="break-all font-mono">{job.upid}</dd>
                </>
              )}
              {job.failure_reason && (
                <>
                  <dt className="text-muted-foreground">Failure</dt>
                  <dd className="text-red-600">{job.failure_reason}</dd>
                </>
              )}
              {job.warnings.length > 0 && (
                <>
                  <dt className="text-muted-foreground">Warnings</dt>
                  <dd>
                    <ul className="list-inside list-disc">
                      {job.warnings.map((w, i) => (
                        <li key={`${w.type}-${String(i)}`}>{w.type.replace(/-/g, " ")}{w.value ? `: ${w.value}` : ""}</li>
                      ))}
                    </ul>
                  </dd>
                </>
              )}
            </dl>
          </td>
        </tr>
      )}
    </>
  );
}
