import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  Loader2,
  RefreshCw,
  Trash2,
  ChevronDown,
  ChevronRight,
  AlertTriangle,
} from "lucide-react";
import {
  useNotificationDLQ,
  useNotificationDLQSummary,
  useRetryNotificationDLQ,
  useDismissNotificationDLQ,
  useDeleteNotificationDLQ,
} from "../api/alert-queries";
import { useAuth } from "@/hooks/useAuth";
import type { DLQState, NotificationDLQEntry } from "@/types/api";

const STATE_VARIANTS: Record<
  DLQState,
  "default" | "secondary" | "destructive" | "outline"
> = {
  pending: "destructive",
  rate_limited: "secondary",
  retrying: "outline",
  resolved: "default",
  dismissed: "secondary",
};

const STATE_LABELS: Record<DLQState, string> = {
  pending: "Failed",
  rate_limited: "Rate-limited",
  retrying: "Retrying",
  resolved: "Resolved",
  dismissed: "Dismissed",
};

function formatRelativeTime(iso: string): string {
  const then = new Date(iso).getTime();
  const seconds = Math.floor((Date.now() - then) / 1000);
  if (seconds < 60) return `${String(seconds)}s ago`;
  if (seconds < 3600) return `${String(Math.floor(seconds / 60))}m ago`;
  if (seconds < 86400) return `${String(Math.floor(seconds / 3600))}h ago`;
  return `${String(Math.floor(seconds / 86400))}d ago`;
}

export function NotificationDLQTable() {
  const { hasPermission } = useAuth();
  const canManage = hasPermission("manage", "notification_dlq");
  const [stateFilter, setStateFilter] = useState<DLQState | "">("");
  const [expanded, setExpanded] = useState<Record<string, boolean>>({});
  const [retryFeedback, setRetryFeedback] = useState<
    Record<string, { success: boolean; message: string } | undefined>
  >({});

  const { data: summary } = useNotificationDLQSummary();
  const { data: entries, isLoading } = useNotificationDLQ(stateFilter);
  const retry = useRetryNotificationDLQ();
  const dismiss = useDismissNotificationDLQ();
  const del = useDeleteNotificationDLQ();

  const toggle = (id: string) => {
    setExpanded((prev) => ({ ...prev, [id]: !prev[id] }));
  };

  const handleRetry = (entry: NotificationDLQEntry) => {
    setRetryFeedback((prev) => ({ ...prev, [entry.id]: undefined }));
    retry.mutate(entry.id, {
      onSuccess: (resp) => {
        setRetryFeedback((prev) => ({
          ...prev,
          [entry.id]: { success: resp.success, message: resp.message },
        }));
      },
      onError: (err) => {
        setRetryFeedback((prev) => ({
          ...prev,
          [entry.id]: {
            success: false,
            message: err instanceof Error ? err.message : "Retry failed",
          },
        }));
      },
    });
  };

  return (
    <div className="space-y-4">
      <div className="grid grid-cols-2 gap-3 md:grid-cols-5">
        <SummaryTile
          label="Failed"
          count={summary?.pending ?? 0}
          variant="destructive"
          onClick={() => { setStateFilter("pending"); }}
          active={stateFilter === "pending"}
        />
        <SummaryTile
          label="Rate-limited"
          count={summary?.rate_limited ?? 0}
          variant="secondary"
          onClick={() => { setStateFilter("rate_limited"); }}
          active={stateFilter === "rate_limited"}
        />
        <SummaryTile
          label="Retrying"
          count={summary?.retrying ?? 0}
          variant="outline"
          onClick={() => { setStateFilter("retrying"); }}
          active={stateFilter === "retrying"}
        />
        <SummaryTile
          label="Resolved"
          count={summary?.resolved ?? 0}
          variant="default"
          onClick={() => { setStateFilter("resolved"); }}
          active={stateFilter === "resolved"}
        />
        <SummaryTile
          label="All"
          count={
            (summary?.pending ?? 0) +
            (summary?.rate_limited ?? 0) +
            (summary?.retrying ?? 0) +
            (summary?.resolved ?? 0) +
            (summary?.dismissed ?? 0)
          }
          variant="outline"
          onClick={() => { setStateFilter(""); }}
          active={stateFilter === ""}
        />
      </div>

      <div className="rounded-md border">
        {isLoading ? (
          <div className="flex items-center justify-center py-12">
            <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
          </div>
        ) : !entries?.length ? (
          <div className="py-12 text-center text-muted-foreground">
            <AlertTriangle className="mx-auto mb-2 h-6 w-6 text-muted-foreground/60" />
            No dead-letter entries
            {stateFilter ? ` in state "${STATE_LABELS[stateFilter]}"` : ""}.
          </div>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="w-8" />
                <TableHead>Channel</TableHead>
                <TableHead>State</TableHead>
                <TableHead>Attempts</TableHead>
                <TableHead>Last error</TableHead>
                <TableHead>Age</TableHead>
                {canManage && (
                  <TableHead className="text-right">Actions</TableHead>
                )}
              </TableRow>
            </TableHeader>
            <TableBody>
              {entries.map((entry) => (
                <DLQRow
                  key={entry.id}
                  entry={entry}
                  expanded={!!expanded[entry.id]}
                  onToggle={() => {
                    toggle(entry.id);
                  }}
                  canManage={canManage}
                  onRetry={() => {
                    handleRetry(entry);
                  }}
                  onDismiss={() => {
                    dismiss.mutate(entry.id);
                  }}
                  onDelete={() => {
                    del.mutate(entry.id);
                  }}
                  retryPending={retry.isPending}
                  feedback={retryFeedback[entry.id]}
                />
              ))}
            </TableBody>
          </Table>
        )}
      </div>
    </div>
  );
}

interface SummaryTileProps {
  label: string;
  count: number;
  variant: "default" | "secondary" | "destructive" | "outline";
  onClick: () => void;
  active: boolean;
}

function SummaryTile({
  label,
  count,
  variant,
  onClick,
  active,
}: SummaryTileProps) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={`rounded-md border bg-card p-3 text-left transition hover:bg-accent ${active ? "ring-2 ring-primary" : ""}`}
    >
      <div className="flex items-center justify-between">
        <span className="text-xs uppercase text-muted-foreground tracking-wide">
          {label}
        </span>
        <Badge variant={variant}>{count}</Badge>
      </div>
    </button>
  );
}

interface DLQRowProps {
  entry: NotificationDLQEntry;
  expanded: boolean;
  onToggle: () => void;
  canManage: boolean;
  onRetry: () => void;
  onDismiss: () => void;
  onDelete: () => void;
  retryPending: boolean;
  feedback?: { success: boolean; message: string } | undefined;
}

function DLQRow({
  entry,
  expanded,
  onToggle,
  canManage,
  onRetry,
  onDismiss,
  onDelete,
  retryPending,
  feedback,
}: DLQRowProps) {
  return (
    <>
      <TableRow className="cursor-pointer" onClick={onToggle}>
        <TableCell className="px-2">
          {expanded ? (
            <ChevronDown className="h-4 w-4" />
          ) : (
            <ChevronRight className="h-4 w-4" />
          )}
        </TableCell>
        <TableCell>
          <div className="font-medium">{entry.channel_name}</div>
          <div className="text-xs text-muted-foreground">
            {entry.channel_type}
          </div>
        </TableCell>
        <TableCell>
          <Badge variant={STATE_VARIANTS[entry.state]}>
            {STATE_LABELS[entry.state]}
          </Badge>
          {feedback && (
            <span
              className={`ml-2 text-xs ${feedback.success ? "text-emerald-600" : "text-red-600"}`}
            >
              {feedback.message}
            </span>
          )}
        </TableCell>
        <TableCell>{entry.attempt_count}</TableCell>
        <TableCell className="max-w-md truncate text-xs text-muted-foreground">
          {entry.last_error}
        </TableCell>
        <TableCell className="text-xs text-muted-foreground">
          {formatRelativeTime(entry.created_at)}
        </TableCell>
        {canManage && (
          <TableCell
            className="text-right"
            onClick={(e) => {
              e.stopPropagation();
            }}
          >
            <div className="flex items-center justify-end gap-1">
              {entry.state !== "resolved" && entry.state !== "dismissed" && (
                <>
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={onRetry}
                    disabled={retryPending}
                  >
                    <RefreshCw className="mr-1 h-3 w-3" />
                    Retry
                  </Button>
                  <Button variant="ghost" size="sm" onClick={onDismiss}>
                    Dismiss
                  </Button>
                </>
              )}
              <Button variant="ghost" size="sm" onClick={onDelete}>
                <Trash2 className="h-4 w-4 text-destructive" />
              </Button>
            </div>
          </TableCell>
        )}
      </TableRow>
      {expanded && (
        <TableRow>
          <TableCell colSpan={canManage ? 7 : 6} className="bg-muted/30">
            <div className="space-y-2 p-2 text-xs">
              <DetailField label="Failure kind" value={entry.failure_kind} />
              <DetailField label="Created" value={entry.created_at} />
              <DetailField label="Last update" value={entry.updated_at} />
              {entry.alert_id && (
                <DetailField label="Alert ID" value={entry.alert_id} />
              )}
              {entry.rule_id && (
                <DetailField label="Rule ID" value={entry.rule_id} />
              )}
              <div>
                <div className="text-muted-foreground mb-1">Last error</div>
                <pre className="whitespace-pre-wrap rounded bg-background p-2 text-xs">
                  {entry.last_error || "(empty)"}
                </pre>
              </div>
              <div>
                <div className="text-muted-foreground mb-1">Stored payload</div>
                <pre className="whitespace-pre-wrap rounded bg-background p-2 text-xs">
                  {JSON.stringify(entry.payload, null, 2)}
                </pre>
              </div>
            </div>
          </TableCell>
        </TableRow>
      )}
    </>
  );
}

function DetailField({ label, value }: { label: string; value: string }) {
  return (
    <div className="grid grid-cols-[160px_1fr] gap-2">
      <span className="text-muted-foreground">{label}</span>
      <span className="font-mono">{value}</span>
    </div>
  );
}
