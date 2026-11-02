import { useState } from "react";
import { BatteryCharging, Plus, Zap } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { EmptyState } from "@/components/EmptyState";
import { QueryFailureNote, QueryStateNotice } from "@/components/QueryStateNotice";
import { usePermissions } from "@/hooks/usePermissions";
import { useDeleteNutServer, useNutServers, useUpsDevices, useUpsEvents } from "../api/ups-queries";
import type { NutServer, UpsDevice } from "../types/ups";
import { formatRuntime, upsOnBattery } from "../lib/ups-state";
import { UpsCard } from "../components/UpsCard";
import { PowerSummary } from "../components/PowerSummary";
import { NutServerTable } from "../components/NutServerTable";
import { NutServerDialog } from "../components/NutServerDialog";
import { LatestEventsNote, PowerEventsTable } from "../components/PowerEventsTable";
import { ConfirmDeleteDialog } from "@/components/ConfirmDeleteDialog";

function OutageBanner({ devices }: { devices: UpsDevice[] }) {
  const onBattery = devices.filter(upsOnBattery);
  if (onBattery.length === 0) return null;
  const shortest = onBattery.reduce((a, b) =>
    (b.battery_runtime ?? Infinity) < (a.battery_runtime ?? Infinity) ? b : a,
  );
  const since = onBattery
    .map((d) => d.on_battery_since)
    .filter((s): s is string => s !== null)
    .sort()[0];
  return (
    <div className="flex items-start gap-3 rounded-lg border border-amber-500/50 bg-amber-500/10 p-4">
      <Zap className="mt-0.5 h-5 w-5 shrink-0 text-amber-600 dark:text-amber-400" aria-hidden="true" />
      <div className="text-sm">
        <div className="font-semibold text-amber-700 dark:text-amber-400">
          {onBattery.length === 1 ? `${onBattery[0]?.name ?? ""} is on battery` : `${String(onBattery.length)} UPS are on battery`}
          {since !== undefined && ` since ${new Date(since).toLocaleTimeString()}`}
        </div>
        <div className="mt-0.5 text-muted-foreground">
          Shortest runtime: {shortest.name}, {formatRuntime(shortest.battery_runtime)} at the current load.
        </div>
      </div>
    </div>
  );
}

/**
 * The Power page: every UPS the NUT servers report, the servers themselves,
 * and the power events Nexara has recorded. Read-only towards the UPS in
 * Phase 1; only the server registry is editable here.
 */
export function PowerPage() {
  const { hasGlobalPermission } = usePermissions();
  const canManage = hasGlobalPermission("manage", "ups");
  const canDelete = hasGlobalPermission("delete", "ups");
  const servers = useNutServers();
  const devices = useUpsDevices();
  const events = useUpsEvents();
  const [editing, setEditing] = useState<NutServer | null>(null);
  const [deleting, setDeleting] = useState<NutServer | null>(null);
  const deleteServer = useDeleteNutServer();

  const serverList = servers.data ?? [];
  const deviceList = devices.data ?? [];
  const noServers = servers.isSuccess && serverList.length === 0;

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-end justify-between gap-4">
        <div>
          <div className="flex items-center gap-3">
            <BatteryCharging className="h-6 w-6 text-primary" aria-hidden="true" />
            <h1 className="text-2xl font-semibold">Power</h1>
          </div>
          <p className="mt-1 text-sm text-muted-foreground">
            UPS devices reported by your Network UPS Tools (NUT) servers
          </p>
        </div>
        {canManage && (
          <NutServerDialog
            trigger={
              <Button>
                <Plus className="mr-2 h-4 w-4" />
                Add NUT server
              </Button>
            }
          />
        )}
      </div>

      {noServers ? (
        <EmptyState
          icon={BatteryCharging}
          title="No NUT servers yet"
          description="Add the upsd that reports your UPS. Nexara reads it like any other client and never logs in as a monitor, so upsmon on your nodes keeps deciding when they shut down."
        />
      ) : (
        <Tabs defaultValue="devices">
          <TabsList>
            <TabsTrigger value="devices">Devices{devices.data === undefined ? "" : ` (${String(deviceList.length)})`}</TabsTrigger>
            <TabsTrigger value="servers">NUT servers{servers.data === undefined ? "" : ` (${String(serverList.length)})`}</TabsTrigger>
            <TabsTrigger value="events">Power events</TabsTrigger>
          </TabsList>

          <TabsContent value="devices" className="space-y-6">
            {deviceList.length > 0 ? (
              <>
                <QueryFailureNote query={devices} subject="the latest UPS readings" />
                <OutageBanner devices={deviceList} />
                <PowerSummary devices={deviceList} />
                <div className="grid gap-4 lg:grid-cols-2">
                  {deviceList.map((d) => (
                    <UpsCard key={d.id} device={d} />
                  ))}
                </div>
              </>
            ) : (
              <QueryStateNotice
                query={devices}
                subject="UPS devices"
                empty="No UPS reported yet. A new server's devices appear within one poll."
              />
            )}
          </TabsContent>

          <TabsContent value="servers" className="space-y-4">
            {serverList.length > 0 ? (
              <>
                <QueryFailureNote query={servers} subject="NUT servers" />
                <NutServerTable
                  servers={serverList}
                  canManage={canManage}
                  canDelete={canDelete}
                  onEdit={setEditing}
                  onDelete={setDeleting}
                />
              </>
            ) : (
              <QueryStateNotice query={servers} subject="NUT servers" empty="No NUT servers yet." />
            )}
            <p className="text-xs text-muted-foreground">
              Nexara reads upsd as an ordinary client. It never logs in as a monitor, so it cannot hold up a
              shutdown, and it needs credentials only where upsd demands them.
            </p>
          </TabsContent>

          <TabsContent value="events">
            {(events.data?.length ?? 0) > 0 ? (
              <>
                <PowerEventsTable events={events.data ?? []} showUps />
                <LatestEventsNote count={events.data?.length ?? 0} />
              </>
            ) : (
              <QueryStateNotice query={events} subject="power events" empty="No power events recorded yet." />
            )}
          </TabsContent>
        </Tabs>
      )}

      {editing !== null && (
        <NutServerDialog
          key={editing.id}
          server={editing}
          open
          onOpenChange={(open) => {
            if (!open) setEditing(null);
          }}
        />
      )}
      <ConfirmDeleteDialog
        target={deleting}
        onClose={() => {
          setDeleting(null);
        }}
        onConfirm={(s) => {
          deleteServer.mutate(s.id);
        }}
        title={(s) => `Remove ${s.name}?`}
        description={() =>
          "Nexara stops reading this server and deletes its stored credential, its UPS devices, and every reading and power event collected from it. Nothing on the NUT server changes."
        }
        confirmLabel="Remove"
      />
    </div>
  );
}
