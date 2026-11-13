import { useState } from "react";
import { Link } from "react-router-dom";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Button } from "@/components/ui/button";
import { Panel } from "@/components/page/Panel";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import {
  Download,
  Eye,
  FileCode,
  Loader2,
  Mail,
  MoreHorizontal,
  Trash2,
} from "lucide-react";
import {
  useReportRuns,
  useDeleteReportRun,
  downloadReportRun,
} from "../api/report-queries";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import { useAuth } from "@/hooks/useAuth";
import type { ReportRun } from "@/types/api";
import { reportTypeLabel, periodLabel, runFileName } from "../report-types";
import { ReportEmailDialog } from "./ReportEmailDialog";

/** A run's state in words. A finished run is the ordinary case and stays plain. */
function RunStatus({ run }: { run: ReportRun }) {
  if (run.status === "completed") {
    return <span className="text-muted-foreground">Completed</span>;
  }
  if (run.status === "running") {
    return (
      <span className="inline-flex items-center gap-1.5">
        <Loader2 className="h-3.5 w-3.5 animate-spin text-data" />
        Running
      </span>
    );
  }
  if (run.status === "failed") {
    return (
      <span className="inline-flex items-center gap-[7px] text-red-700 dark:text-red-400">
        <span
          aria-hidden="true"
          className="h-[7px] w-[7px] shrink-0 rounded-full bg-red-500"
        />
        Failed
      </span>
    );
  }
  return <span className="text-muted-foreground">Pending</span>;
}

export function ReportRunsTable() {
  const { data: runs, isLoading, error } = useReportRuns();
  const { data: clusters } = useClusters();
  const deleteRun = useDeleteReportRun();
  const { hasPermission } = useAuth();
  const canManage = hasPermission("manage", "report");
  const canGenerate = hasPermission("generate", "report");
  const [emailRun, setEmailRun] = useState<ReportRun | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<ReportRun | null>(null);
  const [deleteError, setDeleteError] = useState("");

  const clusterName = (id: string) =>
    clusters?.find((c) => c.id === id)?.name ?? "—";

  const download = (run: ReportRun, format: "html" | "csv") => {
    void downloadReportRun(
      run,
      format,
      runFileName(run, clusterName(run.cluster_id), format),
    );
  };

  return (
    <Panel
      flush
      title="Report history"
      aside={
        runs && runs.length > 0
          ? `${String(runs.length)} ${runs.length === 1 ? "run" : "runs"}`
          : undefined
      }
    >
      {deleteError && (
        <p className="px-[18px] pb-3 text-sm text-destructive">{deleteError}</p>
      )}
      {isLoading ? (
        <p className="px-[18px] pb-1 text-sm text-muted-foreground">
          Loading...
        </p>
      ) : error ? (
        <p className="px-[18px] pb-1 text-sm text-destructive">
          Failed to load report history:{" "}
          {error instanceof Error ? error.message : "Unknown error"}
        </p>
      ) : !runs?.length ? (
        <p className="px-[18px] pb-1 text-sm text-muted-foreground">
          No report runs yet. Generate one from the report types.
        </p>
      ) : (
        <Table>
          <TableHeader>
            <TableRow className="hover:bg-transparent">
              <TableHead>Report</TableHead>
              <TableHead className="hidden sm:table-cell">Period</TableHead>
              <TableHead>Generated</TableHead>
              <TableHead>Status</TableHead>
              <TableHead className="w-12">
                <span className="sr-only">Actions</span>
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {runs.map((run) => {
              const label = reportTypeLabel(run.report_type);
              const completed = run.status === "completed";
              const canDelete = canManage && run.status !== "running";
              return (
                <TableRow key={run.id}>
                  <TableCell className="whitespace-nowrap py-2">
                    <div className="font-medium">
                      {completed ? (
                        <Link
                          to={`/reports/runs/${run.id}`}
                          className="hover:underline"
                        >
                          {label}
                        </Link>
                      ) : (
                        label
                      )}
                    </div>
                    <div className="text-[12.5px] text-muted-foreground">
                      {clusterName(run.cluster_id)}
                    </div>
                  </TableCell>
                  <TableCell className="hidden py-2 whitespace-nowrap sm:table-cell">
                    {periodLabel(run.time_range_hours)}
                  </TableCell>
                  <TableCell className="py-2">
                    <div className="whitespace-nowrap">
                      {new Date(run.created_at).toLocaleString()}
                    </div>
                    <div className="text-[12.5px] text-muted-foreground">
                      {run.schedule_id ? "By a schedule" : "On demand"}
                    </div>
                  </TableCell>
                  <TableCell className="py-2">
                    <RunStatus run={run} />
                    {run.error_message ? (
                      <div
                        className="max-w-[22rem] truncate text-[12.5px] text-red-700 dark:text-red-400"
                        title={run.error_message}
                      >
                        {run.error_message}
                      </div>
                    ) : null}
                  </TableCell>
                  <TableCell className="py-1.5 text-right">
                    {(completed || canDelete) && (
                      <DropdownMenu>
                        <DropdownMenuTrigger asChild>
                          <Button
                            variant="ghost"
                            size="icon"
                            className="h-8 w-8 text-muted-foreground"
                            aria-label={`Actions for ${label}, ${new Date(run.created_at).toLocaleString()}`}
                            title="Actions"
                          >
                            <MoreHorizontal className="h-4 w-4" />
                          </Button>
                        </DropdownMenuTrigger>
                        <DropdownMenuContent align="end">
                          {completed && (
                            <>
                              <DropdownMenuItem asChild>
                                <Link to={`/reports/runs/${run.id}`}>
                                  <Eye className="mr-2 h-4 w-4" />
                                  Open
                                </Link>
                              </DropdownMenuItem>
                              <DropdownMenuItem
                                onSelect={() => {
                                  download(run, "html");
                                }}
                              >
                                <FileCode className="mr-2 h-4 w-4" />
                                Download HTML
                              </DropdownMenuItem>
                              <DropdownMenuItem
                                onSelect={() => {
                                  download(run, "csv");
                                }}
                              >
                                <Download className="mr-2 h-4 w-4" />
                                Download CSV
                              </DropdownMenuItem>
                              {canGenerate && (
                                <DropdownMenuItem
                                  onSelect={() => {
                                    setEmailRun(run);
                                  }}
                                >
                                  <Mail className="mr-2 h-4 w-4" />
                                  Email this report
                                </DropdownMenuItem>
                              )}
                            </>
                          )}
                          {completed && canDelete && <DropdownMenuSeparator />}
                          {canDelete && (
                            <DropdownMenuItem
                              className="text-destructive focus:text-destructive"
                              onSelect={() => {
                                setDeleteTarget(run);
                              }}
                            >
                              <Trash2 className="mr-2 h-4 w-4" />
                              Delete run
                            </DropdownMenuItem>
                          )}
                        </DropdownMenuContent>
                      </DropdownMenu>
                    )}
                  </TableCell>
                </TableRow>
              );
            })}
          </TableBody>
        </Table>
      )}

      <ReportEmailDialog
        run={emailRun}
        onOpenChange={(o) => {
          if (!o) setEmailRun(null);
        }}
      />

      <AlertDialog
        open={deleteTarget !== null}
        onOpenChange={(o) => {
          if (!o) setDeleteTarget(null);
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete this report run?</AlertDialogTitle>
            <AlertDialogDescription>
              {deleteTarget
                ? `${reportTypeLabel(deleteTarget.report_type)} for ${clusterName(deleteTarget.cluster_id)}, created ${new Date(deleteTarget.created_at).toLocaleString()}. The stored HTML and CSV are removed; this cannot be undone.`
                : ""}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              onClick={() => {
                setDeleteError("");
                if (deleteTarget) {
                  // The delete is cluster-scoped on the server while the
                  // button is gated on the global grant, so a refusal must
                  // be shown rather than swallowed.
                  deleteRun.mutate(deleteTarget.id, {
                    onError: (err) => {
                      setDeleteError(
                        err instanceof Error
                          ? `Could not delete the run: ${err.message}`
                          : "Could not delete the run.",
                      );
                    },
                  });
                }
                setDeleteTarget(null);
              }}
            >
              Delete
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </Panel>
  );
}
