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
import type { ChannelType } from "@/types/api";

const CHANNEL_TYPE_LABELS: Record<ChannelType, string> = {
  email: "Email",
  slack: "Slack",
  discord: "Discord",
  teams: "Teams",
  telegram: "Telegram",
  webhook: "Webhook",
  pagerduty: "PagerDuty",
  expo_push: "Mobile push",
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
                    className={`ml-2 text-xs ${testResults[ch.id]?.success ? "text-green-600" : "text-red-600"}`}
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
                      variant="ghost"
                      size="sm"
                      onClick={() => {
                        deleteMutation.mutate(ch.id);
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
    </div>
  );
}
