import { useTranslation } from "react-i18next";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Badge } from "@/components/ui/badge";
import { AlertSummaryCard } from "../components/AlertSummaryCard";
import { AlertsTable } from "../components/AlertsTable";
import { AlertRulesTable } from "../components/AlertRulesTable";
import { AlertRuleForm } from "../components/AlertRuleForm";
import { ChannelsTable } from "../components/ChannelsTable";
import { ChannelForm } from "../components/ChannelForm";
import { NotificationDLQTable } from "../components/NotificationDLQTable";
import { useNotificationDLQSummary } from "../api/alert-queries";
import { useAuth } from "@/hooks/useAuth";

export function AlertsPage() {
  const { t } = useTranslation("alerts");
  const { hasPermission } = useAuth();
  const canManage = hasPermission("manage", "alert");
  const canManageChannels = hasPermission("manage", "notification_channel");
  const canViewDLQ = hasPermission("view", "notification_dlq");
  const { data: dlqSummary } = useNotificationDLQSummary();
  const dlqOpenCount =
    (dlqSummary?.pending ?? 0) + (dlqSummary?.rate_limited ?? 0);

  return (
    <div className="space-y-6 p-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h1 className="text-2xl font-bold tracking-tight">{t("alerts")}</h1>
        <div className="flex flex-wrap gap-2">
          {canManageChannels && <ChannelForm />}
          {canManage && <AlertRuleForm />}
        </div>
      </div>

      <AlertSummaryCard />

      <Tabs defaultValue="alerts">
        <TabsList>
          <TabsTrigger value="alerts">{t("alertHistory")}</TabsTrigger>
          <TabsTrigger value="rules">{t("alertRules")}</TabsTrigger>
          <TabsTrigger value="channels">{t("channels")}</TabsTrigger>
          {canViewDLQ && (
            <TabsTrigger value="dlq" className="gap-2">
              Dead-letter queue
              {dlqOpenCount > 0 && (
                <Badge variant="destructive">{dlqOpenCount}</Badge>
              )}
            </TabsTrigger>
          )}
        </TabsList>

        <TabsContent value="alerts" className="mt-4">
          <AlertsTable />
        </TabsContent>

        <TabsContent value="rules" className="mt-4">
          <AlertRulesTable />
        </TabsContent>

        <TabsContent value="channels" className="mt-4">
          <ChannelsTable />
        </TabsContent>

        {canViewDLQ && (
          <TabsContent value="dlq" className="mt-4">
            <NotificationDLQTable />
          </TabsContent>
        )}
      </Tabs>
    </div>
  );
}
