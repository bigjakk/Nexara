import { useEffect, useRef, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import {
  ChevronDown,
  Download,
  FileCode,
  FileText,
  Mail,
  Printer,
  RefreshCw,
  Trash2,
} from "lucide-react";
import {
  Breadcrumbs,
  DetailHeader,
  Fact,
  StatusFact,
} from "@/components/page/DetailHeader";
import { Panel } from "@/components/page/Panel";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
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

/** How the facts line names a run's state, and the dot it draws for it. */
const RUN_STATE: Record<string, { dot: string; label: string }> = {
  completed: { dot: "running", label: "Completed" },
  failed: { dot: "offline", label: "Failed" },
  running: { dot: "unknown", label: "Generating" },
  pending: { dot: "unknown", label: "Queued" },
};

/**
 * A finished run on its own page: the stored report in a sandboxed frame under
 * a header with the things a reader does with it. The frame shows the same
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
      <div className="space-y-5">
        <Skeleton className="h-16 w-2/3" />
        <Skeleton className="h-[70vh] w-full" />
      </div>
    );
  }
  if (error || !run) {
    return (
      <div className="space-y-3">
        <Breadcrumbs
          crumbs={[{ label: "Reports", to: "/reports" }, { label: "Run" }]}
        />
        <p className="text-destructive">
          {error instanceof Error ? error.message : "Report run not found."}
        </p>
        <Button variant="link" asChild className="px-0">
          <Link to="/reports">Back to reports</Link>
        </Button>
      </div>
    );
  }

  const title = reportTypeLabel(run.report_type);
  const period = periodLabel(run.time_range_hours);
  const state = RUN_STATE[run.status] ?? { dot: "unknown", label: run.status };
  const canDelete = canManage && run.status !== "running";

  return (
    <div className="space-y-5">
      <DetailHeader
        crumbs={[{ label: "Reports", to: "/reports" }, { label: title }]}
        icon={<FileText />}
        title={title}
        facts={
          <>
            <StatusFact status={state.dot} label={state.label} nameDot />
            {clusterName !== undefined && (
              <Fact label="Cluster">
                <Link
                  to={`/clusters/${run.cluster_id}`}
                  className="hover:text-foreground hover:underline"
                >
                  {clusterName}
                </Link>
              </Fact>
            )}
            <Fact label="Period">{period}</Fact>
            <Fact label="Generated">
              {run.completed_at
                ? new Date(run.completed_at).toLocaleString()
                : new Date(run.created_at).toLocaleString()}
            </Fact>
            <Fact label="Source">
              {run.schedule_id ? "A schedule" : "On demand"}
            </Fact>
          </>
        }
        actions={
          <>
            {canGenerate && run.status !== "running" && (
              <Button
                // The one thing to do with a run that has no report to read.
                variant={completed ? "outline" : "default"}
                size="sm"
                onClick={handleRunAgain}
                disabled={generate.isPending}
              >
                <RefreshCw className="mr-2 h-4 w-4" />
                {generate.isPending ? "Running…" : "Run again"}
              </Button>
            )}
            {completed && (
              <Button size="sm" disabled={!frameReady} onClick={handlePrint}>
                <Printer className="mr-2 h-4 w-4" />
                Print / PDF
              </Button>
            )}
            {(completed || canDelete) && (
              <DropdownMenu>
                <DropdownMenuTrigger asChild>
                  <Button variant="outline" size="sm" className="gap-1.5">
                    More
                    <ChevronDown className="h-3.5 w-3.5 opacity-60" />
                  </Button>
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end">
                  {completed && (
                    <>
                      <DropdownMenuItem
                        onSelect={() => {
                          void downloadReportRun(
                            run,
                            "html",
                            runFileName(run, clusterName ?? "cluster", "html"),
                          );
                        }}
                      >
                        <FileCode className="mr-2 h-4 w-4" />
                        Download HTML
                      </DropdownMenuItem>
                      <DropdownMenuItem
                        onSelect={() => {
                          void downloadReportRun(
                            run,
                            "csv",
                            runFileName(run, clusterName ?? "cluster", "csv"),
                          );
                        }}
                      >
                        <Download className="mr-2 h-4 w-4" />
                        Download CSV
                      </DropdownMenuItem>
                      {canGenerate && (
                        <DropdownMenuItem
                          onSelect={() => {
                            setEmailOpen(true);
                          }}
                        >
                          <Mail className="mr-2 h-4 w-4" />
                          Email
                        </DropdownMenuItem>
                      )}
                    </>
                  )}
                  {completed && canDelete && <DropdownMenuSeparator />}
                  {canDelete && (
                    <DropdownMenuItem
                      className="text-destructive focus:text-destructive"
                      onSelect={() => {
                        setConfirmDelete(true);
                      }}
                    >
                      <Trash2 className="mr-2 h-4 w-4" />
                      Delete run
                    </DropdownMenuItem>
                  )}
                </DropdownMenuContent>
              </DropdownMenu>
            )}
          </>
        }
      />

      {run.status === "failed" ? (
        <Panel title="This run failed">
          <p className="text-sm text-muted-foreground">
            {run.error_message ?? "No error message was recorded."}
          </p>
        </Panel>
      ) : !completed ? (
        <Panel>
          <p className="text-sm text-muted-foreground">
            Generating… this page refreshes when the report is ready.
          </p>
        </Panel>
      ) : htmlLoading ? (
        <Panel>
          <p className="text-sm text-muted-foreground">Loading report…</p>
        </Panel>
      ) : html ? (
        // The frame fills what the header leaves of the window and scrolls
        // inside itself, so the header and its actions stay in reach while
        // the report is read.
        <Panel flush bodyClassName="p-0" className="overflow-hidden">
          <iframe
            ref={frameRef}
            srcDoc={html}
            title={`${title} report`}
            className="block h-[calc(100vh-17.5rem)] min-h-[30rem] w-full border-0"
            // allow-scripts is deliberately absent: the report is static
            // markup, so nothing inside it needs to run, and allow-modals
            // grants the parent's print() call without granting the document
            // any ability to open dialogs of its own.
            sandbox="allow-same-origin allow-modals"
            onLoad={() => {
              setFrameReady(true);
            }}
          />
        </Panel>
      ) : (
        <Panel>
          <p className="text-sm text-muted-foreground">
            No report content is stored for this run.
          </p>
        </Panel>
      )}

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
