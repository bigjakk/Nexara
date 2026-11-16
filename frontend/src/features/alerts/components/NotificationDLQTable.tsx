import { useTranslation } from "react-i18next";
import i18n from "@/lib/i18n";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Badge, type BadgeVariant } from "@/components/ui/badge";
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
} from "lucide-react";
import {
  useNotificationDLQ,
  useNotificationDLQSummary,
  useRetryNotificationDLQ,
  useDismissNotificationDLQ,
  useDeleteNotificationDLQ,
} from "../api/alert-queries";
import { useAuth } from "@/hooks/useAuth";
import { useSaveOutcome } from "@/hooks/useSaveOutcome";
import { formatExactDateTime } from "@/lib/format";
import { ConfirmDeleteDialog } from "@/components/ConfirmDeleteDialog";
import { DetailField } from "@/components/DetailField";
import { ExpandedDetailRow } from "@/components/ExpandedDetailRow";
import { DefGrid, Panel } from "@/components/page/Panel";
import { Stat, StatStrip } from "@/components/page/StatStrip";
import { cn } from "@/lib/utils";
import type { DLQState, NotificationDLQEntry } from "@/types/api";
import { DateTime } from "@/components/DateTime";

const STATE_VARIANTS: Record<DLQState, BadgeVariant> = {
  pending: "destructive",
  rate_limited: "secondary",
  retrying: "outline",
  resolved: "outline",
  dismissed: "secondary",
};

const STATE_LABELS: Record<DLQState, string> = {
  get pending() {
    return i18n.t("alerts:notificationDLQTable.statePending");
  },
  get rate_limited() {
    return i18n.t("alerts:notificationDLQTable.stateRateLimited");
  },
  get retrying() {
    return i18n.t("alerts:notificationDLQTable.stateRetrying");
  },
  get resolved() {
    return i18n.t("alerts:notificationDLQTable.stateResolved");
  },
  get dismissed() {
    return i18n.t("alerts:notificationDLQTable.stateDismissed");
  },
};

export function NotificationDLQTable() {
  const { t } = useTranslation("alerts");
  const { hasPermission } = useAuth();
  const canManage = hasPermission("manage", "notification_dlq");
  const [stateFilter, setStateFilter] = useState<DLQState | "">("");
  const [expanded, setExpanded] = useState<Record<string, boolean>>({});
  const [retryFeedback, setRetryFeedback] = useState<
    Record<string, { success: boolean; message: string } | undefined>
  >({});
  const [pendingDelete, setPendingDelete] =
    useState<NotificationDLQEntry | null>(null);

  const { data: summary } = useNotificationDLQSummary();
  const { data: entries, isLoading } = useNotificationDLQ(stateFilter);
  const retry = useRetryNotificationDLQ();
  // Each row shows its own retry's failure (the hook opts out of the app's
  // toast); one that lands after the page was left is toasted instead.
  const settle = useSaveOutcome();
  const dismiss = useDismissNotificationDLQ();
  const del = useDeleteNotificationDLQ();

  const toggle = (id: string) => {
    setExpanded((prev) => ({ ...prev, [id]: !prev[id] }));
  };

  const handleRetry = (entry: NotificationDLQEntry) => {
    setRetryFeedback((prev) => ({ ...prev, [entry.id]: undefined }));
    settle(retry.mutateAsync(entry.id), {
      action: t("notificationDLQTable.retryingTheNotificationToChannelName", {
        channelName: entry.channel_name,
      }),
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
            message:
              err instanceof Error
                ? err.message
                : t("notificationDLQTable.retryFailed"),
          },
        }));
      },
    });
  };

  const counts = {
    pending: summary?.pending ?? 0,
    rate_limited: summary?.rate_limited ?? 0,
    retrying: summary?.retrying ?? 0,
    resolved: summary?.resolved ?? 0,
  };
  const all =
    counts.pending +
    counts.rate_limited +
    counts.retrying +
    counts.resolved +
    (summary?.dismissed ?? 0);
  // No summary (still loading, or the read failed) is not a count of zero.
  const count = (n: number) => (summary === undefined ? "—" : n);
  const pick = (state: DLQState | "") => ({
    selected: stateFilter === state,
    onSelect: () => {
      setStateFilter(state);
    },
  });

  return (
    <Panel flush bodyClassName="pt-4">
      {/* The counts are the filter: the one pressed is what the table lists. */}
      <StatStrip>
        <Stat
          label={t("notificationDLQTable.failed")}
          value={count(counts.pending)}
          tone={counts.pending > 0 ? "bad" : "quiet"}
          {...pick("pending")}
        />
        <Stat
          label={t("notificationDLQTable.rateLimited")}
          value={count(counts.rate_limited)}
          tone={counts.rate_limited > 0 ? "warn" : "quiet"}
          {...pick("rate_limited")}
        />
        <Stat
          label={t("notificationDLQTable.retrying")}
          value={count(counts.retrying)}
          tone={counts.retrying > 0 ? "default" : "quiet"}
          {...pick("retrying")}
        />
        <Stat
          label={t("notificationDLQTable.resolved")}
          value={count(counts.resolved)}
          tone={counts.resolved > 0 ? "default" : "quiet"}
          {...pick("resolved")}
        />
        <Stat
          label={t("notificationDLQTable.all")}
          value={count(all)}
          tone={all > 0 ? "default" : "quiet"}
          {...pick("")}
        />
      </StatStrip>

      <div className="-mt-px border-t">
        {isLoading ? (
          <div className="flex items-center justify-center py-12">
            <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
          </div>
        ) : !entries?.length ? (
          <p className="px-[18px] py-10 text-center text-sm text-muted-foreground">
            {stateFilter
              ? t("notificationDLQTable.noDeadLetterEntriesInState", {
                  state: STATE_LABELS[stateFilter],
                })
              : t("notificationDLQTable.noDeadLetterEntries")}
          </p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="w-8" />
                <TableHead>{t("notificationDLQTable.channel")}</TableHead>
                <TableHead>{t("notificationDLQTable.state")}</TableHead>
                <TableHead>{t("notificationDLQTable.attempts")}</TableHead>
                <TableHead>{t("notificationDLQTable.lastError")}</TableHead>
                <TableHead>{t("notificationDLQTable.age")}</TableHead>
                {canManage && (
                  <TableHead className="text-right">
                    {t("notificationDLQTable.actions")}
                  </TableHead>
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
                    setPendingDelete(entry);
                  }}
                  retryPending={retry.isPending}
                  feedback={retryFeedback[entry.id]}
                />
              ))}
            </TableBody>
          </Table>
        )}
      </div>
      <ConfirmDeleteDialog
        target={pendingDelete}
        onClose={() => {
          setPendingDelete(null);
        }}
        onConfirm={(entry) => {
          del.mutate(entry.id);
        }}
        title={(entry) =>
          t("notificationDLQTable.deleteTheFailedNotificationTo", {
            channelName: entry.channel_name,
            createdAt: formatExactDateTime(entry.created_at, "—", "relative"),
          })
        }
        description={() => t("notificationDLQTable.theEntryItsStoredPayload")}
      />
    </Panel>
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
  const { t } = useTranslation("alerts");
  return (
    <>
      <TableRow className="cursor-pointer" onClick={onToggle}>
        <TableCell className="text-muted-foreground">
          {expanded ? (
            <ChevronDown className="h-4 w-4" />
          ) : (
            <ChevronRight className="h-4 w-4" />
          )}
        </TableCell>
        <TableCell>
          <div className="font-medium">{entry.channel_name}</div>
          <div className="text-[12.5px] text-muted-foreground">
            {entry.channel_type}
          </div>
        </TableCell>
        <TableCell>
          <Badge variant={STATE_VARIANTS[entry.state]}>
            {STATE_LABELS[entry.state]}
          </Badge>
          {feedback && (
            <span
              className={cn(
                "ml-2 text-[12.5px]",
                feedback.success
                  ? "text-muted-foreground"
                  : "text-red-700 dark:text-red-400",
              )}
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
          <DateTime value={entry.created_at} auto="relative" />
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
                    className="gap-1"
                    onClick={onRetry}
                    disabled={retryPending}
                  >
                    <RefreshCw className="h-3 w-3" />
                    {t("notificationDLQTable.retry")}
                  </Button>
                  <Button variant="ghost" size="sm" onClick={onDismiss}>
                    {t("notificationDLQTable.dismiss")}
                  </Button>
                </>
              )}
              <Button
                aria-label={t(
                  "notificationDLQTable.deleteFailedNotificationToChannelName",
                  {
                    channelName: entry.channel_name,
                    createdAt: formatExactDateTime(
                      entry.created_at,
                      "—",
                      "relative",
                    ),
                  },
                )}
                variant="ghost"
                size="sm"
                className="h-8 px-2 text-muted-foreground hover:text-red-700 dark:hover:text-red-400"
                onClick={onDelete}
              >
                <Trash2 className="h-4 w-4" />
              </Button>
            </div>
          </TableCell>
        )}
      </TableRow>
      {expanded && (
        <ExpandedDetailRow colSpan={canManage ? 7 : 6}>
          <DefGrid className="grid-cols-[repeat(auto-fit,minmax(min(14rem,100%),1fr))]">
            <DetailField
              label={t("notificationDLQTable.failureKind")}
              variant="mono"
            >
              {entry.failure_kind}
            </DetailField>
            <DetailField
              label={t("notificationDLQTable.created")}
              variant="mono"
            >
              <DateTime value={entry.created_at} exact auto="as-sent" />
            </DetailField>
            <DetailField
              label={t("notificationDLQTable.lastUpdate")}
              variant="mono"
            >
              <DateTime value={entry.updated_at} exact auto="as-sent" />
            </DetailField>
            {entry.alert_id && (
              <DetailField
                label={t("notificationDLQTable.alertID")}
                variant="mono"
              >
                {entry.alert_id}
              </DetailField>
            )}
            {entry.rule_id && (
              <DetailField
                label={t("notificationDLQTable.ruleID")}
                variant="mono"
              >
                {entry.rule_id}
              </DetailField>
            )}
          </DefGrid>
          <div className="text-xs">
            <div className="mb-1 text-muted-foreground">
              {t("notificationDLQTable.lastError")}
            </div>
            <pre className="whitespace-pre-wrap rounded-md bg-background p-2.5">
              {entry.last_error || t("notificationDLQTable.empty")}
            </pre>
          </div>
          <div className="text-xs">
            <div className="mb-1 text-muted-foreground">
              {t("notificationDLQTable.storedPayload")}
            </div>
            {entry.payload === null ? (
              <p className="text-muted-foreground">{t("dlqPayloadHidden")}</p>
            ) : (
              <pre className="whitespace-pre-wrap rounded-md bg-background p-2.5">
                {JSON.stringify(entry.payload, null, 2)}
              </pre>
            )}
          </div>
        </ExpandedDetailRow>
      )}
    </>
  );
}
