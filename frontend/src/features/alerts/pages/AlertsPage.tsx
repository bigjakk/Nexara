import { useTranslation } from "react-i18next";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Badge } from "@/components/ui/badge";
import { PageHeader } from "@/components/page/PageHeader";
import { AlertSummaryStrip } from "../components/AlertSummaryStrip";
import { AlertsTable } from "../components/AlertsTable";
import { AlertRulesTable } from "../components/AlertRulesTable";
import { AlertRuleForm } from "../components/AlertRuleForm";
import { ChannelsTable } from "../components/ChannelsTable";
import { ChannelForm } from "../components/ChannelForm";
import { NotificationDLQTable } from "../components/NotificationDLQTable";
import {
  useAlertSummary,
  useNotificationDLQSummary,
} from "../api/alert-queries";
import { alertSummaryLine } from "../lib/alert-summary";
import { useAuth } from "@/hooks/useAuth";
import { usePermissions } from "@/hooks/usePermissions";

export function AlertsPage() {
  const { t } = useTranslation("alerts");
  const { hasPermission } = useAuth();
  const { hasGlobalPermission } = usePermissions();
  const canManage = hasPermission("manage", "alert");
  // The channel and dead-letter routes take their grants install-wide
  // (registry_alerts.go, registry_notification_dlq.go).
  const canManageChannels = hasGlobalPermission(
    "manage",
    "notification_channel",
  );
  const canViewDLQ = hasGlobalPermission("view", "notification_dlq");
  const { data: summary } = useAlertSummary();
  // The summary takes the same install-wide read: no polling for 403s.
  const { data: dlqSummary } = useNotificationDLQSummary(canViewDLQ);
  const dlqOpenCount =
    (dlqSummary?.pending ?? 0) + (dlqSummary?.rate_limited ?? 0);

  return (
    <div className="space-y-5">
      <PageHeader
        title={t("alerts")}
        summary={alertSummaryLine(summary, t)}
        actions={
          canManage || canManageChannels ? (
            <>
              {canManageChannels && <ChannelForm />}
              {canManage && <AlertRuleForm />}
            </>
          ) : undefined
        }
      />

      <AlertSummaryStrip summary={summary} />

      <Tabs defaultValue="alerts">
        <TabsList>
          <TabsTrigger value="alerts">{t("alertHistory")}</TabsTrigger>
          <TabsTrigger value="rules">{t("alertRules")}</TabsTrigger>
          <TabsTrigger value="channels">{t("channels")}</TabsTrigger>
          {canViewDLQ && (
            <TabsTrigger value="dlq" className="gap-2">
              {t("deadLetterQueue")}
              {dlqOpenCount > 0 && (
                <Badge variant="destructive">{dlqOpenCount}</Badge>
              )}
            </TabsTrigger>
          )}
        </TabsList>

        <TabsContent value="alerts">
          <AlertsTable />
        </TabsContent>

        <TabsContent value="rules">
          <AlertRulesTable />
        </TabsContent>

        <TabsContent value="channels">
          <ChannelsTable />
        </TabsContent>

        {canViewDLQ && (
          <TabsContent value="dlq">
            <NotificationDLQTable />
          </TabsContent>
        )}
      </Tabs>
    </div>
  );
}
