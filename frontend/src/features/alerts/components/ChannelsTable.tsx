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
import { Loader2, Play, Trash2 } from "lucide-react";
import {
  useNotificationChannels,
  useDeleteNotificationChannel,
  useTestNotificationChannel,
} from "../api/alert-queries";
import { ChannelForm } from "./ChannelForm";
import { useAuth } from "@/hooks/useAuth";
import { ConfirmDeleteDialog } from "@/components/ConfirmDeleteDialog";
import type { ChannelType, NotificationChannel } from "@/types/api";

const CHANNEL_TYPE_LABELS: Record<ChannelType, string> = {
  email: "Email",
  slack: "Slack",
  discord: "Discord",
  teams: "Teams",
  telegram: "Telegram",
  webhook: "Webhook",
  pagerduty: "PagerDuty",
};

export function ChannelsTable() {
  const { hasPermission } = useAuth();
  const canManage = hasPermission("manage", "notification_channel");
  const { data: channels, isLoading } = useNotificationChannels();
  const deleteMutation = useDeleteNotificationChannel();
  const testMutation = useTestNotificationChannel();
  const [testResults, setTestResults] = useState<
    Record<string, { success: boolean; message: string } | undefined>
  >({});
  const [pendingDelete, setPendingDelete] =
    useState<NotificationChannel | null>(null);

  const handleTest = (id: string) => {
    setTestResults((prev) => ({ ...prev, [id]: undefined }));
    testMutation.mutate(id, {
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
            message: err instanceof Error ? err.message : "Test failed",
          },
        }));
      },
    });
  };

  if (isLoading) {
    return (
      <div className="flex items-center justify-center py-8">
        <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
      </div>
    );
  }

  if (!channels?.length) {
    return (
      <div className="text-center py-8 text-muted-foreground">
        No notification channels configured.
        {canManage && " Create one to start receiving alert notifications."}
      </div>
    );
  }

  return (
    <div className="rounded-md border">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Name</TableHead>
            <TableHead>Type</TableHead>
            <TableHead>Status</TableHead>
            <TableHead>Created</TableHead>
            {canManage && <TableHead className="text-right">Actions</TableHead>}
          </TableRow>
        </TableHeader>
        <TableBody>
          {channels.map((ch) => (
            <TableRow key={ch.id}>
              <TableCell className="font-medium">{ch.name}</TableCell>
              <TableCell>
                <Badge variant="outline">
                  {CHANNEL_TYPE_LABELS[ch.channel_type]}
                </Badge>
              </TableCell>
              <TableCell>
                <Badge variant={ch.enabled ? "default" : "secondary"}>
                  {ch.enabled ? "Enabled" : "Disabled"}
                </Badge>
                {testResults[ch.id] != null && (
                  <span
                    className={`ml-2 text-xs ${testResults[ch.id]?.success ? "text-emerald-600" : "text-red-600"}`}
                  >
                    {testResults[ch.id]?.message}
                  </span>
                )}
              </TableCell>
              <TableCell className="text-muted-foreground text-sm">
                {new Date(ch.created_at).toLocaleDateString()}
              </TableCell>
              {canManage && (
                <TableCell className="text-right">
                  <div className="flex items-center justify-end gap-1">
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={() => {
                        handleTest(ch.id);
                      }}
                      disabled={testMutation.isPending}
                    >
                      <Play className="mr-1 h-3 w-3" />
                      Test
                    </Button>
                    <ChannelForm
                      editId={ch.id}
                      editName={ch.name}
                      editType={ch.channel_type}
                      editEnabled={ch.enabled}
                    />
                    <Button
                      aria-label={`Delete ${ch.name}`}
                      variant="ghost"
                      size="sm"
                      onClick={() => {
                        setPendingDelete(ch);
                      }}
                      disabled={deleteMutation.isPending}
                    >
                      <Trash2 className="h-4 w-4 text-destructive" />
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
        title={(ch) => `Delete notification channel ${ch.name}?`}
        // What references a channel (migrations): alert rules only through
        // their escalation_chain JSONB, which has no foreign key, so the step
        // stays and notifications.Engine.dispatchToChannel logs "channel not
        // found or disabled" and returns without a DLQ row.
        // cve_notification_config_channels is ON DELETE CASCADE (000057);
        // report_schedules.email_channel_id (000024) and
        // rolling_update_jobs.notify_channel_id (000031) are SET NULL; so is
        // notification_dlq.channel_id (000053), and ReplayDLQ then refuses
        // with "channel deleted; cannot replay".
        description={() =>
          "Its stored settings are deleted and cannot be recovered. Alert rules whose escalation chain uses this channel keep that step, but nothing is sent through it any more. It is removed from CVE notification settings, report schedules and rolling-update jobs that notify through it are left with no channel, and its failed notifications can no longer be retried."
        }
      />
    </div>
  );
}
