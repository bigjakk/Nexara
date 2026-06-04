import { Activity } from "lucide-react";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { usePermissions } from "@/hooks/usePermissions";
import { AuditLogPanel } from "../components/AuditLogPanel";
import { TasksPanel } from "@/features/tasks/components/TasksPanel";

/**
 * EventsPage hosts the two activity views as tabs: the Audit Log (every recorded
 * action, persisted) and Tasks (the Proxmox task lifecycle + outcomes). The
 * Tasks tab is shown only to users with view:task; the route is gated on
 * view:audit (all built-in roles have both).
 */
export function EventsPage() {
  const { canView } = usePermissions();
  const canTasks = canView("task");

  return (
    <div className="space-y-4 p-6">
      <div className="flex items-center gap-2">
        <Activity className="h-6 w-6 text-primary" />
        <h1 className="text-2xl font-bold">Events</h1>
      </div>

      <Tabs defaultValue="audit">
        <TabsList>
          <TabsTrigger value="audit">Audit Log</TabsTrigger>
          {canTasks && <TabsTrigger value="tasks">Tasks</TabsTrigger>}
        </TabsList>

        <TabsContent value="audit" className="mt-4">
          <AuditLogPanel />
        </TabsContent>

        {canTasks && (
          <TabsContent value="tasks" className="mt-4">
            <TasksPanel />
          </TabsContent>
        )}
      </Tabs>
    </div>
  );
}
