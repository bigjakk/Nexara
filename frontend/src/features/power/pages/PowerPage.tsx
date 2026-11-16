import { useState } from "react";
import { BatteryCharging, Plus, Zap } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { EmptyState } from "@/components/EmptyState";
import { PageHeader } from "@/components/page/PageHeader";
import { Panel } from "@/components/page/Panel";
import { QueryFailureNote, QueryStateNotice } from "@/components/QueryStateNotice";
import { usePermissions } from "@/hooks/usePermissions";
import { useDeleteNutServer, useNutServers, useUpsDevices, useUpsEvents } from "../api/ups-queries";
import type { NutServer, UpsDevice } from "../types/ups";
import { formatRuntime, upsOnBattery } from "../lib/ups-state";
import { feedName, protectionSummary } from "../lib/ups-feeds";
import { powerSummaryLine } from "../lib/power-summary";
import { UpsList } from "../components/UpsList";
import { PowerSummary } from "../components/PowerSummary";
import { NutServerTable } from "../components/NutServerTable";
import { NutServerDialog } from "../components/NutServerDialog";
import { LatestEventsNote, PowerEventsTable } from "../components/PowerEventsTable";
import { ConfirmDeleteDialog } from "@/components/ConfirmDeleteDialog";
import { formatTime } from "@/lib/format";
import { useTranslation } from "react-i18next";

function OutageBanner({ devices }: { devices: UpsDevice[] }) {
  const { t } = useTranslation("power");
  const onBattery = devices.filter(upsOnBattery);
  if (onBattery.length === 0) return null;
  const shortest = onBattery.reduce((a, b) =>
    (b.battery_runtime ?? Infinity) < (a.battery_runtime ?? Infinity) ? b : a,
  );
  // A node another UPS, still on mains, also powers does not lose power.
  const stillFed = new Set(
    devices.filter((d) => d.state === "online").flatMap((d) => d.feeds.map(feedName)),
  );
  const unprotected = protectionSummary(onBattery).unprotected.filter((n) => !stillFed.has(n));
  const since = onBattery
    .map((d) => d.on_battery_since)
    .filter((s): s is string => s !== null)
    .sort()[0];
  return (
    // The one loud thing on the page: an outage keeps its outline and tint.
    <div className="flex items-start gap-3 rounded-[10px] border border-amber-500/50 bg-amber-500/10 px-[18px] py-3.5">
      <Zap className="mt-0.5 h-5 w-5 shrink-0 text-amber-600 dark:text-amber-400" aria-hidden="true" />
      <div className="min-w-0 text-sm">
        <div className="text-[15px] font-semibold text-amber-700 dark:text-amber-400">
          {onBattery.length === 1
            ? since === undefined
              ? t("powerPage.nameIsOnBattery", { name: onBattery[0]?.name ?? "" })
              : t("powerPage.nameIsOnBatterySince", { name: onBattery[0]?.name ?? "", since: formatTime(since) })
            : since === undefined
              ? t("powerPage.lengthUPSAreOnBattery", { length: String(onBattery.length) })
              : t("powerPage.lengthUPSAreOnBatterySince", { length: String(onBattery.length), since: formatTime(since) })}
        </div>
        <div className="mt-0.5 text-muted-foreground">
          {t("powerPage.shortestRuntimeNameBatteryRuntimeAt", { name: shortest.name, batteryRuntime: formatRuntime(shortest.battery_runtime) })}
        </div>
        {unprotected.length > 0 && (
          <div className="mt-0.5 font-medium text-amber-700 dark:text-amber-400">
            {t("powerPage.willLosePowerWithoutShutting", { unprotected: unprotected.join(", ") })}
          </div>
        )}
      </div>
    </div>
  );
}

/**
 * The Power page: every UPS the NUT servers report, the servers themselves,
 * and the power events Nexara has recorded. The outage and the headline
 * figures sit above the tabs, so they show whichever list is open.
 */
export function PowerPage() {
  const { t } = useTranslation("power");
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
  const eventList = events.data ?? [];
  const noServers = servers.isSuccess && serverList.length === 0;

  return (
    <div className="space-y-5">
      <PageHeader
        title={t("powerPage.power")}
        summary={
          devices.data !== undefined && servers.data !== undefined && !noServers
            ? powerSummaryLine(deviceList, serverList.length)
            : t("powerPage.upsDevicesReportedByYour")
        }
        actions={
          canManage ? (
            <NutServerDialog
              trigger={
                <Button className="gap-1.5">
                  <Plus className="h-4 w-4" aria-hidden="true" />
                  {t("powerPage.addNUTServer")}
                </Button>
              }
            />
          ) : undefined
        }
      />

      {noServers ? (
        <EmptyState
          icon={BatteryCharging}
          title={t("powerPage.noNUTServersYet")}
          description={t("powerPage.addTheUpsdThatReports")}
        />
      ) : (
        <>
          {deviceList.length > 0 && (
            <>
              <QueryFailureNote query={devices} subject={t("powerPage.theLatestUPSReadings")} />
              <OutageBanner devices={deviceList} />
              <PowerSummary devices={deviceList} />
            </>
          )}
          <Tabs defaultValue="devices">
            <TabsList>
              <TabsTrigger value="devices">
                {devices.data === undefined
                  ? t("powerPage.devices")
                  : t("powerPage.devicesCount", { number: String(deviceList.length) })}
              </TabsTrigger>
              <TabsTrigger value="servers">
                {servers.data === undefined
                  ? t("powerPage.nutServersTab")
                  : t("powerPage.nutServersTabCount", { number: String(serverList.length) })}
              </TabsTrigger>
              <TabsTrigger value="events">{t("powerPage.powerEvents")}</TabsTrigger>
            </TabsList>

            <TabsContent value="devices">
              {deviceList.length > 0 ? (
                <Panel flush bodyClassName="pb-1">
                  <UpsList devices={deviceList} />
                </Panel>
              ) : (
                <Panel>
                  <QueryStateNotice
                    query={devices}
                    subject={t("powerPage.upsDevices")}
                    empty={t("powerPage.noUPSReportedYetA")}
                  />
                </Panel>
              )}
            </TabsContent>

            <TabsContent value="servers">
              {serverList.length > 0 ? (
                <Panel flush>
                  <QueryFailureNote query={servers} subject={t("powerPage.nutServers")} className="px-[18px] pb-2" />
                  <NutServerTable
                    servers={serverList}
                    canManage={canManage}
                    canDelete={canDelete}
                    onEdit={setEditing}
                    onDelete={setDeleting}
                  />
                  <p className="border-t px-[18px] pt-3 text-[12.5px] text-muted-foreground">
                    {t("powerPage.nexaraReadsUpsdAsAn")}
                  </p>
                </Panel>
              ) : (
                <Panel>
                  <QueryStateNotice query={servers} subject={t("powerPage.nutServers")} empty={t("powerPage.noNUTServersYet2")} />
                </Panel>
              )}
            </TabsContent>

            <TabsContent value="events">
              {eventList.length > 0 ? (
                <Panel flush>
                  <PowerEventsTable events={eventList} showUps />
                  <LatestEventsNote count={eventList.length} />
                </Panel>
              ) : (
                <Panel>
                  <QueryStateNotice query={events} subject={t("powerPage.powerEvents2")} empty={t("powerPage.noPowerEventsRecordedYet")} />
                </Panel>
              )}
            </TabsContent>
          </Tabs>
        </>
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
        title={(s) => t("powerPage.removeName", { name: s.name })}
        description={() => t("powerPage.nexaraStopsReadingThisServer")}
        confirmLabel={t("powerPage.remove")}
      />
    </div>
  );
}
