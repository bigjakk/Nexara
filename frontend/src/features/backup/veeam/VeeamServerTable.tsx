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
import { Button } from "@/components/ui/button";
import {
  ChevronDown,
  ChevronRight,
  Pencil,
  PlugZap,
  Trash2,
} from "lucide-react";
import { useTestVeeamServer } from "../api/backup-queries";
import type { VeeamProbeResult, VeeamServer } from "../types/backup";
import { VeeamProbeSummary } from "./VeeamProbeSummary";
import { VeeamServerStatusBadge } from "./VeeamServerStatusBadge";

interface VeeamServerTableProps {
  servers: VeeamServer[];
  onEdit: (server: VeeamServer) => void;
  onDelete: (server: VeeamServer) => void;
}

const REQUIRED_EDITION = "EnterprisePlus";

function formatTimestamp(value: string | null): string {
  if (value == null || value === "") return "Never";
  const parsed = new Date(value);
  if (Number.isNaN(parsed.getTime())) return value;
  return parsed.toLocaleString();
}

export function VeeamServerTable({
  servers,
  onEdit,
  onDelete,
}: VeeamServerTableProps) {
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  // Per-server test results, keyed by id: one server's probe must not clear
  // another's, and a shared mutation result would do exactly that.
  const [probes, setProbes] = useState<Record<string, VeeamProbeResult>>({});
  const [probeErrors, setProbeErrors] = useState<Record<string, string>>({});
  const [testingId, setTestingId] = useState<string | null>(null);

  const testServer = useTestVeeamServer();

  function toggleExpand(id: string) {
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(id)) {
        next.delete(id);
      } else {
        next.add(id);
      }
      return next;
    });
  }

  function handleTest(server: VeeamServer) {
    setTestingId(server.id);
    // Drop any error from a previous attempt on this server, without
    // disturbing the others.
    setProbeErrors((prev) =>
      Object.fromEntries(
        Object.entries(prev).filter(([id]) => id !== server.id),
      ),
    );

    testServer.mutate(server.id, {
      onSuccess: (result) => {
        setProbes((prev) => ({ ...prev, [server.id]: result }));
        // Show the answer without making the operator hunt for it.
        setExpanded((prev) => new Set(prev).add(server.id));
      },
      onError: (err) => {
        setProbeErrors((prev) => ({
          ...prev,
          [server.id]:
            err instanceof Error ? err.message : "Connection test failed",
        }));
        setExpanded((prev) => new Set(prev).add(server.id));
      },
      onSettled: () => {
        setTestingId(null);
      },
    });
  }

  return (
    <div className="rounded-md border">
      <div className="overflow-x-auto">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-8" />
              <TableHead>Name</TableHead>
              <TableHead>Address</TableHead>
              <TableHead>Version</TableHead>
              <TableHead>Edition</TableHead>
              <TableHead>Status</TableHead>
              <TableHead className="w-32 text-right">Actions</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {servers.map((server) => {
              const isExpanded = expanded.has(server.id);
              const probe = probes[server.id];
              const probeError = probeErrors[server.id];
              const editionOK = server.license_edition === REQUIRED_EDITION;

              return (
                <Fragment key={server.id}>
                  <TableRow
                    className="cursor-pointer"
                    onClick={() => {
                      toggleExpand(server.id);
                    }}
                  >
                    <TableCell className="px-2">
                      {isExpanded ? (
                        <ChevronDown className="h-4 w-4" />
                      ) : (
                        <ChevronRight className="h-4 w-4" />
                      )}
                    </TableCell>
                    <TableCell className="font-medium">{server.name}</TableCell>
                    <TableCell className="break-all font-mono text-xs">
                      {server.base_url}
                    </TableCell>
                    <TableCell className="font-mono text-sm">
                      {server.product_version || "-"}
                    </TableCell>
                    <TableCell>
                      {server.license_edition === "" ? (
                        <span className="text-muted-foreground">-</span>
                      ) : (
                        <Badge variant={editionOK ? "default" : "secondary"}>
                          {server.license_edition}
                        </Badge>
                      )}
                    </TableCell>
                    <TableCell>
                      <VeeamServerStatusBadge server={server} />
                    </TableCell>
                    <TableCell
                      className="text-right"
                      onClick={(e) => {
                        e.stopPropagation();
                      }}
                    >
                      <Button
                        variant="ghost"
                        size="sm"
                        title="Test connection"
                        disabled={testingId === server.id}
                        onClick={() => {
                          handleTest(server);
                        }}
                      >
                        <PlugZap className="h-4 w-4" />
                      </Button>
                      <Button
                        variant="ghost"
                        size="sm"
                        title="Edit"
                        onClick={() => {
                          onEdit(server);
                        }}
                      >
                        <Pencil className="h-4 w-4" />
                      </Button>
                      <Button
                        variant="ghost"
                        size="sm"
                        title="Delete"
                        onClick={() => {
                          onDelete(server);
                        }}
                      >
                        <Trash2 className="h-4 w-4" />
                      </Button>
                    </TableCell>
                  </TableRow>

                  {isExpanded && (
                    <TableRow>
                      <TableCell colSpan={7} className="bg-muted/30">
                        <div className="space-y-4 px-2 py-3">
                          <dl className="grid gap-x-6 gap-y-2 text-sm sm:grid-cols-2 lg:grid-cols-3">
                            <div>
                              <dt className="text-xs text-muted-foreground">
                                Username
                              </dt>
                              {/* Withheld from read-only callers: it is half
                                  of a domain administrator credential. */}
                              <dd
                                className={
                                  server.username === ""
                                    ? "text-muted-foreground"
                                    : "font-mono"
                                }
                              >
                                {server.username === ""
                                  ? "Hidden — requires manage:veeam"
                                  : server.username}
                              </dd>
                            </div>
                            <div>
                              <dt className="text-xs text-muted-foreground">
                                API revision
                              </dt>
                              <dd className="font-mono">
                                {server.api_revision || "-"}
                              </dd>
                            </div>
                            <div>
                              <dt className="text-xs text-muted-foreground">
                                Certificate
                              </dt>
                              <dd>
                                {server.tls_fingerprint !== ""
                                  ? "Pinned (SHA-256)"
                                  : server.verify_tls
                                    ? "Verified against system CAs"
                                    : "Not verified"}
                              </dd>
                            </div>
                            <div>
                              <dt className="text-xs text-muted-foreground">
                                Last sync
                              </dt>
                              <dd>{formatTimestamp(server.last_sync_at)}</dd>
                            </div>
                            <div>
                              <dt className="text-xs text-muted-foreground">
                                Added
                              </dt>
                              <dd>{formatTimestamp(server.created_at)}</dd>
                            </div>
                          </dl>

                          {server.tls_fingerprint !== "" && (
                            <div>
                              <p className="mb-1 text-xs text-muted-foreground">
                                Pinned fingerprint
                              </p>
                              <code className="select-all break-all font-mono text-xs">
                                {server.tls_fingerprint}
                              </code>
                            </div>
                          )}

                          {server.last_sync_error !== "" && (
                            <p className="text-sm text-destructive">
                              Last sync failed: {server.last_sync_error}
                            </p>
                          )}

                          {probeError != null && (
                            <p className="text-sm text-destructive">
                              {probeError}
                            </p>
                          )}

                          {probe != null && probeError == null && (
                            <VeeamProbeSummary probe={probe} />
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
