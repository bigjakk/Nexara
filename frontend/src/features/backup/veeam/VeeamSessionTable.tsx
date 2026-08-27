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
import { VeeamSessionActions } from "./VeeamSessionActions";
import { VeeamSessionLog } from "./VeeamSessionLog";
import type { VeeamSession } from "../types/backup";

interface VeeamSessionTableProps {
  sessions: VeeamSession[];
  /** The server these runs belong to. Stopping one posts against it. */
  serverId: string;
  /** Identifies which server these rows belong to, so expansion state resets. */
  scopeKey?: string;
}

function formatTime(value: string | null): string {
  if (value == null || value === "") return "—";
  const parsed = new Date(value);
  if (Number.isNaN(parsed.getTime())) return value;
  return parsed.toLocaleString();
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

export function VeeamSessionTable({
  sessions,
  serverId,
  scopeKey = "",
}: VeeamSessionTableProps) {
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  // Row ids are server-scoped, so switching servers must not carry a stale
  // expansion set forward — it only grows, and rows silently re-expand on
  // return.
  const [expandedFor, setExpandedFor] = useState(scopeKey);
  if (expandedFor !== scopeKey) {
    setExpandedFor(scopeKey);
    setExpanded(new Set());
  }

  if (sessions.length === 0) {
    return (
      <p className="py-8 text-center text-sm text-muted-foreground">
        No Proxmox backup runs recorded yet.
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
              <TableHead>State</TableHead>
              <TableHead>Result</TableHead>
              <TableHead>Mode</TableHead>
              <TableHead>Started</TableHead>
              <TableHead>Duration</TableHead>
              <TableHead className="text-right">Transferred</TableHead>
              <TableHead className="text-right">Actions</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {sessions.map((session) => {
              const isExpanded = expanded.has(session.id);

              return (
                <Fragment key={session.id}>
                  <TableRow
                    className="cursor-pointer"
                    onClick={() => {
                      toggle(session.id);
                    }}
                  >
                    <TableCell className="px-2">
                      {isExpanded ? (
                        <ChevronDown className="h-4 w-4" />
                      ) : (
                        <ChevronRight className="h-4 w-4" />
                      )}
                    </TableCell>
                    <TableCell className="font-medium">
                      {session.name}
                    </TableCell>
                    <TableCell>
                      <Badge variant="secondary">{session.state || "-"}</Badge>
                    </TableCell>
                    <TableCell>
                      {session.result === "" ? (
                        <span className="text-muted-foreground">—</span>
                      ) : (
                        <Badge variant={resultVariant(session.result)}>
                          {/* Never a bare "Failed": Veeam records an
                              API-cancelled run identically to a real failure.
                              Keyed on nexara_STOPPED, not nexara_initiated — a
                              run Nexara STARTED can fail for a completely real
                              reason, and labelling that "stopped" would tell an
                              operator to ignore a genuine backup failure. */}
                          {session.result === "Failed"
                            ? session.nexara_stopped
                              ? "Stopped from Nexara"
                              : "Failed or cancelled"
                            : session.result}
                        </Badge>
                      )}
                    </TableCell>
                    <TableCell className="text-sm">
                      {session.algorithm || "—"}
                    </TableCell>
                    <TableCell className="text-sm">
                      {formatTime(session.creation_time)}
                    </TableCell>
                    <TableCell className="font-mono text-sm">
                      {session.duration || "—"}
                    </TableCell>
                    <TableCell className="text-right font-mono text-sm">
                      {formatBytes(session.transferred_size)}
                    </TableCell>
                    <TableCell className="text-right">
                      <VeeamSessionActions
                        serverId={serverId}
                        session={session}
                      />
                    </TableCell>
                  </TableRow>

                  {isExpanded && (
                    <TableRow>
                      <TableCell colSpan={9} className="bg-muted/30">
                        <div className="space-y-3 px-2 py-3">
                          <dl className="grid gap-x-6 gap-y-2 text-sm sm:grid-cols-2 lg:grid-cols-4">
                            <div>
                              <dt className="text-xs text-muted-foreground">
                                Finished
                              </dt>
                              <dd>{formatTime(session.end_time)}</dd>
                            </div>
                            <div>
                              <dt className="text-xs text-muted-foreground">
                                Bottleneck
                              </dt>
                              <dd>
                                {session.bottleneck === "" ||
                                session.bottleneck === "NotDefined"
                                  ? "—"
                                  : session.bottleneck}
                              </dd>
                            </div>
                            <div>
                              <dt className="text-xs text-muted-foreground">
                                Processing rate
                              </dt>
                              <dd className="font-mono">
                                {session.processing_rate === "" ||
                                session.processing_rate === "N/A"
                                  ? "—"
                                  : session.processing_rate}
                              </dd>
                            </div>
                            <div>
                              <dt className="text-xs text-muted-foreground">
                                Initiated by
                              </dt>
                              <dd>{session.initiated_by || "—"}</dd>
                            </div>
                            <div>
                              <dt className="text-xs text-muted-foreground">
                                Processed
                              </dt>
                              <dd className="font-mono">
                                {formatBytes(session.processed_size)}
                              </dd>
                            </div>
                            <div>
                              <dt className="text-xs text-muted-foreground">
                                Read
                              </dt>
                              <dd className="font-mono">
                                {formatBytes(session.read_size)}
                              </dd>
                            </div>
                          </dl>

                          {session.result_message !== "" &&
                            session.result_message !== "Success" && (
                              <p className="text-sm text-muted-foreground">
                                {session.result_message}
                              </p>
                            )}

                          {session.result === "Failed" &&
                            !session.nexara_stopped && (
                              <p className="text-xs text-muted-foreground">
                                Veeam records a run stopped through its API the
                                same way it records a genuine failure, with no
                                cancellation flag and an empty log — so this may
                                have been someone stopping the job from the
                                Veeam console.
                              </p>
                            )}

                          {/* Fetched only once the row is open: each read
                              costs a fresh logon against the Veeam server. */}
                          <VeeamSessionLog
                            serverId={serverId}
                            session={session}
                            enabled={isExpanded}
                          />
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
