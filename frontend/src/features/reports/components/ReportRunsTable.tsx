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
import { Badge, type BadgeVariant } from "@/components/ui/badge";
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
import { Eye, Download, FileCode, Mail, Trash2 } from "lucide-react";
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

const STATUS_VARIANTS: Record<string, BadgeVariant> = {
  completed: "default",
  running: "outline",
  pending: "secondary",
  failed: "destructive",
};

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

  if (isLoading)
    return (
      <div className="py-8 text-center text-muted-foreground">Loading...</div>
    );
  if (error)
    return (
      <div className="py-8 text-center text-destructive">
        Failed to load report history:{" "}
        {error instanceof Error ? error.message : "Unknown error"}
      </div>
    );
  if (!runs?.length)
    return (
      <div className="py-8 text-center text-muted-foreground">
        No report runs yet. Generate one from the catalogue above.
      </div>
    );

  return (
    <>
      {deleteError && (
        <p className="mb-2 text-sm text-destructive">{deleteError}</p>
      )}
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Report</TableHead>
            <TableHead>Cluster</TableHead>
            <TableHead>Status</TableHead>
            <TableHead>Period</TableHead>
            <TableHead>Created</TableHead>
            <TableHead>Source</TableHead>
            <TableHead className="w-[200px]">Actions</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {runs.map((run) => (
            <TableRow key={run.id}>
              <TableCell className="font-medium">
                {run.status === "completed" ? (
                  <Link
                    to={`/reports/runs/${run.id}`}
                    className="hover:underline"
                  >
                    {reportTypeLabel(run.report_type)}
                  </Link>
                ) : (
                  reportTypeLabel(run.report_type)
                )}
              </TableCell>
              <TableCell>{clusterName(run.cluster_id)}</TableCell>
              <TableCell>
                <Badge variant={STATUS_VARIANTS[run.status] ?? "secondary"}>
                  {run.status}
                </Badge>
                {run.error_message ? (
                  <span
                    className="ml-2 text-xs text-destructive"
                    title={run.error_message}
                  >
                    {run.error_message.length > 60
                      ? run.error_message.slice(0, 60) + "…"
                      : run.error_message}
                  </span>
                ) : null}
              </TableCell>
              <TableCell>{periodLabel(run.time_range_hours)}</TableCell>
              <TableCell className="text-xs text-muted-foreground">
                {new Date(run.created_at).toLocaleString()}
              </TableCell>
              <TableCell className="text-xs text-muted-foreground">
                {run.schedule_id ? "Schedule" : "On demand"}
              </TableCell>
              <TableCell>
                <div className="flex gap-1">
                  {run.status === "completed" && (
                    <>
                      <Button variant="ghost" size="icon" asChild title="Open">
                        <Link to={`/reports/runs/${run.id}`}>
                          <Eye className="h-4 w-4" />
                        </Link>
                      </Button>
                      <Button
                        variant="ghost"
                        size="icon"
                        title="Download HTML"
                        onClick={() => {
                          void downloadReportRun(
                            run,
                            "html",
                            runFileName(
                              run,
                              clusterName(run.cluster_id),
                              "html",
                            ),
                          );
                        }}
                      >
                        <FileCode className="h-4 w-4" />
                      </Button>
                      <Button
                        variant="ghost"
                        size="icon"
                        title="Download CSV"
                        onClick={() => {
                          void downloadReportRun(
                            run,
                            "csv",
                            runFileName(
                              run,
                              clusterName(run.cluster_id),
                              "csv",
                            ),
                          );
                        }}
                      >
                        <Download className="h-4 w-4" />
                      </Button>
                      {canGenerate && (
                        <Button
                          variant="ghost"
                          size="icon"
                          title="Email this report"
                          onClick={() => {
                            setEmailRun(run);
                          }}
                        >
                          <Mail className="h-4 w-4" />
                        </Button>
                      )}
                    </>
                  )}
                  {canManage && run.status !== "running" && (
                    <Button
                      variant="ghost"
                      size="icon"
                      title="Delete run"
                      onClick={() => {
                        setDeleteTarget(run);
                      }}
                    >
                      <Trash2 className="h-4 w-4 text-destructive" />
                    </Button>
                  )}
                </div>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>

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
    </>
  );
}
