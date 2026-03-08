import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { AlertSummaryCard } from "../components/AlertSummaryCard";
import { AlertsTable } from "../components/AlertsTable";
import { AlertRulesTable } from "../components/AlertRulesTable";
import { AlertRuleForm } from "../components/AlertRuleForm";
import { ChannelsTable } from "../components/ChannelsTable";
import { ChannelForm } from "../components/ChannelForm";
import { useAuth } from "@/hooks/useAuth";

export function AlertsPage() {
  const { hasPermission } = useAuth();
  const canManage = hasPermission("manage", "alert");
  const canManageChannels = hasPermission("manage", "notification_channel");

  return (
    <div className="space-y-6 p-6">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-bold">Alerts</h1>
        <div className="flex gap-2">
          {canManageChannels && <ChannelForm />}
          {canManage && <AlertRuleForm />}
        </div>
      </div>

      <AlertSummaryCard />

      <Tabs defaultValue="alerts">
        <TabsList>
          <TabsTrigger value="alerts">Alert History</TabsTrigger>
          <TabsTrigger value="rules">Alert Rules</TabsTrigger>
          <TabsTrigger value="channels">Channels</TabsTrigger>
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
      </Tabs>
    </div>
  );
}
