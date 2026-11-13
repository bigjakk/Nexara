import { PageHeader } from "@/components/page/PageHeader";
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
    <div className="space-y-5">
      <PageHeader
        title="Events"
        summary={
          canTasks
            ? "Every action recorded in Nexara or seen in Proxmox, and how each Proxmox task ended."
            : "Every action recorded in Nexara or seen in Proxmox, newest first."
        }
      />

      <Tabs defaultValue="audit">
        <TabsList>
          <TabsTrigger value="audit">Audit log</TabsTrigger>
          {canTasks && <TabsTrigger value="tasks">Tasks</TabsTrigger>}
        </TabsList>

        <TabsContent value="audit" className="mt-5">
          <AuditLogPanel />
        </TabsContent>

        {canTasks && (
          <TabsContent value="tasks" className="mt-5">
            <TasksPanel />
          </TabsContent>
        )}
      </Tabs>
    </div>
  );
}
