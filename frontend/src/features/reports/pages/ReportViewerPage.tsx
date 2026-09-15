import { useEffect, useRef, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import {
  ArrowLeft,
  Download,
  FileCode,
  Mail,
  Printer,
  RefreshCw,
  Trash2,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/skeleton";
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
import { useAuth } from "@/hooks/useAuth";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import {
  useReportRun,
  useReportRunHTML,
  useGenerateReport,
  useDeleteReportRun,
  downloadReportRun,
} from "../api/report-queries";
import { reportTypeLabel, periodLabel, runFileName } from "../report-types";
import { ReportEmailDialog } from "../components/ReportEmailDialog";

/**
 * A finished run on its own page: the stored report in a sandboxed frame with
 * a toolbar for the things a reader does with it. The frame shows the same
 * HTML the schedule emails and the print button hands to the browser, so
 * there is exactly one rendering to get right.
 */
export function ReportViewerPage() {
  const { runId = "" } = useParams<{ runId: string }>();
  const navigate = useNavigate();
  const { hasPermission } = useAuth();
  const { data: run, isLoading, error } = useReportRun(runId);
  const { data: clusters } = useClusters();
  const generate = useGenerateReport();
  const deleteRun = useDeleteReportRun();
  const [emailOpen, setEmailOpen] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);

  const completed = run?.status === "completed";
  const { data: html, isLoading: htmlLoading } = useReportRunHTML(
    completed ? runId : "",
  );
  const frameRef = useRef<HTMLIFrameElement>(null);
  const [frameReady, setFrameReady] = useState(false);

  // srcDoc swaps in place when the run changes, and a stale `true` here would
  // let Print fire against the previous report's document.
  useEffect(() => {
    setFrameReady(false);
  }, [runId, html]);

  const clusterName = clusters?.find((c) => c.id === run?.cluster_id)?.name;
  const canGenerate = run ? hasPermission("generate", "report") : false;
  const canManage = run ? hasPermission("manage", "report") : false;

  /**
   * Hand the report to the browser's print dialog, which is also its
   * "Save as PDF" path. Printing the iframe rather than the page means the
   * report's own print stylesheet applies alone — no app shell. The sandbox
   * carries allow-modals for this: print() counts as a modal, and without
   * the flag the call is swallowed with no error.
   */
  const handlePrint = () => {
    frameRef.current?.contentWindow?.print();
  };

  const handleRunAgain = () => {
    if (!run) return;
    generate.mutate(
      {
        report_type: run.report_type,
        cluster_id: run.cluster_id,
        time_range_hours: run.time_range_hours,
        parameters: run.parameters,
      },
      {
        onSuccess: (next) => {
          void navigate(`/reports/runs/${next.id}`);
        },
      },
    );
  };

  if (isLoading) {
    return (
      <div className="space-y-4 p-6">
        <Skeleton className="h-10 w-2/3" />
        <Skeleton className="h-[70vh] w-full" />
      </div>
    );
  }
  if (error || !run) {
    return (
      <div className="space-y-4 p-6">
        <Button variant="ghost" size="sm" asChild>
          <Link to="/reports">
            <ArrowLeft className="mr-2 h-4 w-4" />
            Reports
          </Link>
        </Button>
        <p className="text-destructive">
          {error instanceof Error ? error.message : "Report run not found."}
        </p>
      </div>
    );
  }

  const title = reportTypeLabel(run.report_type);
  const period = periodLabel(run.time_range_hours);

  return (
    <div className="flex h-full flex-col">
      {/*
        Sticky, because the app shell lets the page scroll behind the report
        frame: without this a reader who scrolls the report loses the toolbar
        and has to scroll back up to print, download or run again.
      */}
      <div className="sticky top-0 z-10 flex flex-wrap items-center gap-2 border-b bg-background px-4 py-3">
        <Button variant="ghost" size="sm" asChild>
          <Link to="/reports">
            <ArrowLeft className="mr-2 h-4 w-4" />
            Reports
          </Link>
        </Button>
        <div className="flex min-w-0 flex-wrap items-center gap-2">
          <h1 className="truncate text-base font-semibold">
            {title}
            {clusterName ? ` · ${clusterName}` : ""}
          </h1>
          <Badge variant="outline">{period}</Badge>
          <Badge
            variant={run.status === "failed" ? "destructive" : "secondary"}
          >
            {run.status}
          </Badge>
          <span className="text-xs text-muted-foreground">
            {run.completed_at
              ? new Date(run.completed_at).toLocaleString()
              : new Date(run.created_at).toLocaleString()}
          </span>
        </div>
        <div className="ml-auto flex flex-wrap gap-1">
          {completed && (
            <>
              <Button
                variant="outline"
                size="sm"
                disabled={!frameReady}
                onClick={handlePrint}
              >
                <Printer className="mr-2 h-4 w-4" />
                Print / PDF
              </Button>
              <Button
                variant="outline"
                size="sm"
                onClick={() => {
                  void downloadReportRun(
                    run,
                    "html",
                    runFileName(run, clusterName ?? "cluster", "html"),
                  );
                }}
              >
                <FileCode className="mr-2 h-4 w-4" />
                HTML
              </Button>
              <Button
                variant="outline"
                size="sm"
                onClick={() => {
                  void downloadReportRun(
                    run,
                    "csv",
                    runFileName(run, clusterName ?? "cluster", "csv"),
                  );
                }}
              >
                <Download className="mr-2 h-4 w-4" />
                CSV
              </Button>
              {canGenerate && (
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => {
                    setEmailOpen(true);
                  }}
                >
                  <Mail className="mr-2 h-4 w-4" />
                  Email
                </Button>
              )}
            </>
          )}
          {canGenerate && run.status !== "running" && (
            <Button
              size="sm"
              onClick={handleRunAgain}
              disabled={generate.isPending}
            >
              <RefreshCw className="mr-2 h-4 w-4" />
              {generate.isPending ? "Running…" : "Run again"}
            </Button>
          )}
          {canManage && run.status !== "running" && (
            <Button
              variant="ghost"
              size="sm"
              title="Delete run"
              onClick={() => {
                setConfirmDelete(true);
              }}
            >
              <Trash2 className="h-4 w-4 text-destructive" />
            </Button>
          )}
        </div>
      </div>

      <div className="flex-1 bg-muted/30">
        {run.status === "failed" ? (
          <div className="p-6">
            <p className="text-sm font-medium text-destructive">
              This run failed.
            </p>
            <p className="mt-1 text-sm text-muted-foreground">
              {run.error_message ?? "No error message was recorded."}
            </p>
          </div>
        ) : !completed ? (
          <div className="p-6 text-sm text-muted-foreground">
            Generating… this page refreshes when the report is ready.
          </div>
        ) : htmlLoading ? (
          <div className="p-6 text-sm text-muted-foreground">
            Loading report…
          </div>
        ) : html ? (
          <iframe
            ref={frameRef}
            srcDoc={html}
            title={`${title} report`}
            className="h-[calc(100vh-7.5rem)] w-full border-0"
            // allow-scripts is deliberately absent: the report is static
            // markup, so nothing inside it needs to run, and allow-modals
            // grants the parent's print() call without granting the document
            // any ability to open dialogs of its own.
            sandbox="allow-same-origin allow-modals"
            onLoad={() => {
              setFrameReady(true);
            }}
          />
        ) : (
          <div className="p-6 text-sm text-muted-foreground">
            No report content is stored for this run.
          </div>
        )}
      </div>

      <ReportEmailDialog
        run={emailOpen ? run : null}
        onOpenChange={(o) => {
          if (!o) setEmailOpen(false);
        }}
      />
      <AlertDialog open={confirmDelete} onOpenChange={setConfirmDelete}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete this report run?</AlertDialogTitle>
            <AlertDialogDescription>
              The stored HTML and CSV are removed; this cannot be undone.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              onClick={() => {
                deleteRun.mutate(run.id, {
                  onSuccess: () => {
                    void navigate("/reports");
                  },
                });
              }}
            >
              Delete
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}
