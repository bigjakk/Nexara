import { Fragment, useState } from "react";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";
import { ChevronDown, ChevronRight } from "lucide-react";
import { formatBytes } from "@/lib/format";
import type { VeeamJob } from "../types/backup";

interface VeeamJobTableProps {
  jobs: VeeamJob[];
  /** Identifies which server these rows belong to, so expansion state resets. */
  scopeKey?: string;
}

/**
 * "Failed or cancelled", never a bare "Failed".
 *
 * Veeam records a job cancelled through its own API as result "Failed" with
 * isCanceled false and an empty session log, so there is genuinely no way to
 * tell an operator's stop from a real failure. Labelling it "Failed" would be
 * a confident wrong answer.
 */
function resultLabel(result: string): string {
  return result === "Failed" ? "Failed or cancelled" : result;
}

function resultVariant(
  result: string,
): "default" | "secondary" | "destructive" | "outline" {
  switch (result) {
    case "Success":
      return "default";
    case "Warning":
      return "outline";
    case "Failed":
      return "destructive";
    default:
      return "secondary";
  }
}

function formatTime(value: string | null): string {
  if (value == null || value === "") return "Never";
  const parsed = new Date(value);
  if (Number.isNaN(parsed.getTime())) return value;
  return parsed.toLocaleString();
}

export function VeeamJobTable({ jobs, scopeKey = "" }: VeeamJobTableProps) {
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  // Row ids are server-scoped, so switching servers must not carry a stale
  // expansion set forward — it only grows, and rows silently re-expand on
  // return.
  const [expandedFor, setExpandedFor] = useState(scopeKey);
  if (expandedFor !== scopeKey) {
    setExpandedFor(scopeKey);
    setExpanded(new Set());
  }

  if (jobs.length === 0) {
    return (
      <p className="py-8 text-center text-sm text-muted-foreground">
        No Proxmox backup jobs found on this server.
      </p>
    );
  }

  function toggle(id: string) {
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }

  return (
    <div className="rounded-md border">
      <div className="overflow-x-auto">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-8" />
              <TableHead>Job</TableHead>
              <TableHead>Status</TableHead>
              <TableHead>Last Result</TableHead>
              <TableHead>Last Run</TableHead>
              <TableHead>Next Run</TableHead>
              <TableHead>Repository</TableHead>
              <TableHead className="text-right">Guests</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {jobs.map((job) => {
              const isExpanded = expanded.has(job.id);
              const running = job.status === "Running";

              return (
                <Fragment key={job.id}>
                  <TableRow
                    className="cursor-pointer"
                    onClick={() => {
                      toggle(job.id);
                    }}
                  >
                    <TableCell className="px-2">
                      {isExpanded ? (
                        <ChevronDown className="h-4 w-4" />
                      ) : (
                        <ChevronRight className="h-4 w-4" />
                      )}
                    </TableCell>
                    <TableCell className="font-medium">{job.name}</TableCell>
                    <TableCell>
                      {running ? (
                        <div className="flex items-center gap-2">
                          <Badge variant="default">Running</Badge>
                          <div className="h-1.5 w-16 overflow-hidden rounded-full bg-muted">
                            <div
                              className="h-full rounded-full bg-primary transition-all"
                              style={{
                                width: `${String(Math.min(Math.max(job.progress_percent, 0), 100))}%`,
                              }}
                            />
                          </div>
                        </div>
                      ) : (
                        <Badge variant="secondary">{job.status || "-"}</Badge>
                      )}
                    </TableCell>
                    <TableCell>
                      {job.last_result === "" ? (
                        <span className="text-muted-foreground">-</span>
                      ) : (
                        <Badge variant={resultVariant(job.last_result)}>
                          {resultLabel(job.last_result)}
                        </Badge>
                      )}
                    </TableCell>
                    <TableCell className="text-sm">
                      {formatTime(job.last_run)}
                    </TableCell>
                    <TableCell className="text-sm">
                      {formatTime(job.next_run)}
                    </TableCell>
                    <TableCell className="text-sm">
                      {job.repository_name || "-"}
                    </TableCell>
                    <TableCell className="text-right">
                      {job.objects_count}
                    </TableCell>
                  </TableRow>

                  {isExpanded && (
                    <TableRow>
                      <TableCell colSpan={8} className="bg-muted/30">
                        <div className="space-y-3 px-2 py-3">
                          {job.description !== "" && (
                            <p className="text-sm text-muted-foreground">
                              {job.description}
                            </p>
                          )}
                          <dl className="grid gap-x-6 gap-y-2 text-sm sm:grid-cols-2 lg:grid-cols-4">
                            <div>
                              <dt className="text-xs text-muted-foreground">
                                Bottleneck
                              </dt>
                              <dd>
                                {job.bottleneck === "" ||
                                job.bottleneck === "NotDefined"
                                  ? "—"
                                  : job.bottleneck}
                              </dd>
                            </div>
                            <div>
                              <dt className="text-xs text-muted-foreground">
                                Duration
                              </dt>
                              <dd className="font-mono">
                                {job.duration || "—"}
                              </dd>
                            </div>
                            <div>
                              <dt className="text-xs text-muted-foreground">
                                Processing rate
                              </dt>
                              <dd className="font-mono">
                                {job.processing_rate === "" ||
                                job.processing_rate === "N/A"
                                  ? "—"
                                  : job.processing_rate}
                              </dd>
                            </div>
                            <div>
                              <dt className="text-xs text-muted-foreground">
                                Schedule
                              </dt>
                              <dd>{job.next_run_policy || "—"}</dd>
                            </div>
                            <div>
                              <dt className="text-xs text-muted-foreground">
                                Processed
                              </dt>
                              <dd className="font-mono">
                                {formatBytes(job.processed_size)}
                              </dd>
                            </div>
                            <div>
                              <dt className="text-xs text-muted-foreground">
                                Read
                              </dt>
                              <dd className="font-mono">
                                {formatBytes(job.read_size)}
                              </dd>
                            </div>
                            <div>
                              <dt className="text-xs text-muted-foreground">
                                Transferred
                              </dt>
                              <dd className="font-mono">
                                {formatBytes(job.transferred_size)}
                              </dd>
                            </div>
                            <div>
                              <dt className="text-xs text-muted-foreground">
                                Job type
                              </dt>
                              <dd>{job.job_type}</dd>
                            </div>
                          </dl>
                          {job.last_result === "Failed" && (
                            <p className="text-xs text-muted-foreground">
                              Veeam records a job stopped through its API the
                              same way it records a genuine failure — same
                              result, no cancellation flag, empty log — so
                              Nexara cannot tell the two apart.
                            </p>
                          )}
                        </div>
                      </TableCell>
                    </TableRow>
                  )}
                </Fragment>
              );
            })}
          </TableBody>
        </Table>
      </div>
    </div>
  );
}
