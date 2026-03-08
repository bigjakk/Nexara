import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { AlertSummaryCard } from "../components/AlertSummaryCard";
import { AlertsTable } from "../components/AlertsTable";
import { AlertRulesTable } from "../components/AlertRulesTable";
import { AlertRuleForm } from "../components/AlertRuleForm";
import { useAuth } from "@/hooks/useAuth";

export function AlertsPage() {
  const { hasPermission } = useAuth();
  const canManage = hasPermission("manage", "alert");

  return (
    <div className="space-y-6 p-6">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-bold">Alerts</h1>
        {canManage && <AlertRuleForm />}
      </div>

      <AlertSummaryCard />

      <Tabs defaultValue="alerts">
        <TabsList>
          <TabsTrigger value="alerts">Alert History</TabsTrigger>
          <TabsTrigger value="rules">Alert Rules</TabsTrigger>
        </TabsList>

        <TabsContent value="alerts" className="mt-4">
          <AlertsTable />
        </TabsContent>

        <TabsContent value="rules" className="mt-4">
          <AlertRulesTable />
        </TabsContent>
      </Tabs>
    </div>
  );
}
