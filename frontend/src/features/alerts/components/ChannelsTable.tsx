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
import { Loader2, Play, Send, Trash2 } from "lucide-react";
import {
  useNotificationChannels,
  useDeleteNotificationChannel,
  useTestNotificationChannel,
} from "../api/alert-queries";
import { ChannelForm } from "./ChannelForm";
import { usePermissions } from "@/hooks/usePermissions";
import { useSaveOutcome } from "@/hooks/useSaveOutcome";
import { ConfirmDeleteDialog } from "@/components/ConfirmDeleteDialog";
import { EmptyState } from "@/components/EmptyState";
import { Panel } from "@/components/page/Panel";
import { QueryStateNotice } from "@/components/QueryStateNotice";
import { cn } from "@/lib/utils";
import type { ChannelType, NotificationChannel } from "@/types/api";
import { DateTime } from "@/components/DateTime";
import { useTranslation } from "react-i18next";
import i18n from "@/lib/i18n";

const CHANNEL_TYPE_LABELS: Record<ChannelType, string> = {
  get email() {
    return i18n.t("alerts:channelsTable.typeEmail");
  },
  get slack() {
    return i18n.t("alerts:channelsTable.typeSlack");
  },
  get discord() {
    return i18n.t("alerts:channelsTable.typeDiscord");
  },
  get teams() {
    return i18n.t("alerts:channelsTable.typeTeams");
  },
  get telegram() {
    return i18n.t("alerts:channelsTable.typeTelegram");
  },
  get webhook() {
    return i18n.t("alerts:channelsTable.typeWebhook");
  },
  get pagerduty() {
    return i18n.t("alerts:channelsTable.typePagerduty");
  },
};

export function ChannelsTable() {
  const { t } = useTranslation("alerts");
  // Adding, testing, editing and deleting a channel take
  // manage:notification_channel install-wide (registry_alerts.go).
  const { hasGlobalPermission } = usePermissions();
  const canManage = hasGlobalPermission("manage", "notification_channel");
  const channelsQuery = useNotificationChannels();
  const { data: channels, isLoading } = channelsQuery;
  const deleteMutation = useDeleteNotificationChannel();
  const testMutation = useTestNotificationChannel();
  // Each row shows its own test's failure (the hook opts out of the app's
  // toast); one that lands after the page was left is toasted instead.
  const settle = useSaveOutcome();
  const [testResults, setTestResults] = useState<
    Record<string, { success: boolean; message: string } | undefined>
  >({});
  const [pendingDelete, setPendingDelete] =
    useState<NotificationChannel | null>(null);

  const handleTest = (id: string) => {
    setTestResults((prev) => ({ ...prev, [id]: undefined }));
    const name = channels?.find((ch) => ch.id === id)?.name ?? id;
    settle(testMutation.mutateAsync(id), {
      action: t("channelsTable.testingTheChannelName", { name }),
      onSuccess: (data) => {
        setTestResults((prev) => ({
          ...prev,
          [id]: { success: data.success, message: data.message },
        }));
      },
      onError: (err) => {
        setTestResults((prev) => ({
          ...prev,
          [id]: {
            success: false,
            message:
              err instanceof Error
                ? err.message
                : t("channelsTable.testFailed"),
          },
        }));
      },
    });
  };

  if (isLoading) {
    return (
      <Panel>
        <div className="flex items-center justify-center py-8">
          <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
        </div>
      </Panel>
    );
  }

  if (!channels?.length) {
    // Only a read that came back empty may say there are none: one that
    // failed, or never ran, says that instead.
    if (!channelsQuery.isSuccess) {
      return (
        <Panel>
          <QueryStateNotice
            query={channelsQuery}
            subject={t("channelsTable.notificationChannels")}
            empty={t("channelsTable.noNotificationChannels")}
          />
        </Panel>
      );
    }
    return (
      <EmptyState
        icon={Send}
        title={t("channelsTable.noNotificationChannels2")}
        description={
          canManage
            ? t("channelsTable.alertsSentNowhereAdd")
            : t("channelsTable.alertsSentNowhere")
        }
      />
    );
  }

  const enabledCount = channels.filter((ch) => ch.enabled).length;

  return (
    <Panel
      flush
      aside={t("channelsTable.channelsEnabled", {
        count: channels.length,
        enabled: String(enabledCount),
      })}
    >
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>{t("channelsTable.name")}</TableHead>
            <TableHead>{t("channelsTable.type")}</TableHead>
            <TableHead>{t("channelsTable.status")}</TableHead>
            <TableHead>{t("channelsTable.created")}</TableHead>
            {canManage && (
              <TableHead className="text-right">
                {t("channelsTable.actions")}
              </TableHead>
            )}
          </TableRow>
        </TableHeader>
        <TableBody>
          {channels.map((ch) => (
            <TableRow key={ch.id}>
              <TableCell className="font-medium">{ch.name}</TableCell>
              <TableCell>{CHANNEL_TYPE_LABELS[ch.channel_type]}</TableCell>
              <TableCell>
                <Badge variant={ch.enabled ? "healthy" : "secondary"}>
                  {ch.enabled
                    ? t("channelsTable.enabled")
                    : t("channelsTable.disabled")}
                </Badge>
                {testResults[ch.id] != null && (
                  <span
                    className={cn(
                      "ml-2 text-[12.5px]",
                      testResults[ch.id]?.success
                        ? "text-muted-foreground"
                        : "text-red-700 dark:text-red-400",
                    )}
                  >
                    {testResults[ch.id]?.message}
                  </span>
                )}
              </TableCell>
              <TableCell className="text-muted-foreground">
                <DateTime value={ch.created_at} kind="date" />
              </TableCell>
              {canManage && (
                <TableCell className="text-right">
                  <div className="flex items-center justify-end gap-1">
                    <Button
                      variant="outline"
                      size="sm"
                      className="gap-1"
                      onClick={() => {
                        handleTest(ch.id);
                      }}
                      disabled={testMutation.isPending}
                    >
                      <Play className="h-3 w-3" />
                      {t("channelsTable.test")}
                    </Button>
                    <ChannelForm
                      editId={ch.id}
                      editName={ch.name}
                      editType={ch.channel_type}
                      editEnabled={ch.enabled}
                    />
                    <Button
                      aria-label={t("channelsTable.deleteName", {
                        name: ch.name,
                      })}
                      variant="ghost"
                      size="sm"
                      className="h-8 px-2 text-muted-foreground hover:text-red-700 dark:hover:text-red-400"
                      onClick={() => {
                        setPendingDelete(ch);
                      }}
                      disabled={deleteMutation.isPending}
                    >
                      <Trash2 className="h-4 w-4" />
                    </Button>
                  </div>
                </TableCell>
              )}
            </TableRow>
          ))}
        </TableBody>
      </Table>
      <ConfirmDeleteDialog
        target={pendingDelete}
        onClose={() => {
          setPendingDelete(null);
        }}
        onConfirm={(ch) => {
          deleteMutation.mutate(ch.id);
        }}
        title={(ch) =>
          t("channelsTable.deleteNotificationChannelName", { name: ch.name })
        }
        // What references a channel (migrations): alert rules only through
        // their escalation_chain JSONB, which has no foreign key, so the step
        // stays and notifications.Engine.dispatchToChannel logs "channel not
        // found or disabled" and returns without a DLQ row.
        // cve_notification_config_channels is ON DELETE CASCADE (000057);
        // report_schedules.email_channel_id (000024) and
        // rolling_update_jobs.notify_channel_id (000031) are SET NULL; so is
        // notification_dlq.channel_id (000053), and ReplayDLQ then refuses
        // with "channel deleted; cannot replay".
        description={() => t("channelsTable.itsStoredSettingsAreDeleted")}
      />
    </Panel>
  );
}
