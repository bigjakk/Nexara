import { Link } from "react-router-dom";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { QueryFailureNote, QueryStateNotice } from "@/components/QueryStateNotice";
import { usePermissions } from "@/hooks/usePermissions";
import { useUpsDevices } from "../api/ups-queries";
import { formatRuntime } from "../lib/ups-state";
import { UpsStatusBadges } from "./UpsBadges";
import { useTranslation, Trans } from "react-i18next";

/** The dashboard's UPS widget: each UPS's status, charge and runtime left. */
export function UpsStatusWidget() {
  const { t } = useTranslation("power");
  const devices = useUpsDevices();
  const list = devices.data ?? [];
  // Adding a NUT server takes manage:ups install-wide.
  const canAddServer = usePermissions().hasGlobalPermission("manage", "ups");

  return (
    <Card className="h-full">
      <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
        <CardTitle className="text-sm font-medium">{t("upsStatusWidget.upsStatus")}</CardTitle>
        <Link to="/power" className="text-xs text-muted-foreground hover:underline">
          {t("upsStatusWidget.power")}
        </Link>
      </CardHeader>
      <CardContent className="space-y-2">
        {list.length > 0 ? (
          <>
            <QueryFailureNote query={devices} subject={t("upsStatusWidget.theLatestUPSReadings")} />
            <ul className="divide-y">
              {list.map((d) => {
                const live = d.state !== "unknown";
                return (
                  <li key={d.id} className="flex flex-wrap items-center gap-x-3 gap-y-1 py-2">
                    <Link to={`/power/${d.id}`} className="min-w-0 truncate text-sm font-medium hover:underline">
                      {d.name}
                    </Link>
                    <UpsStatusBadges device={d} />
                    <span className="ml-auto text-xs tabular-nums text-muted-foreground">
                      {live && d.battery_charge !== null ? `${String(Math.round(d.battery_charge))}%` : "—"}
                      {" · "}
                      {live ? formatRuntime(d.battery_runtime) : "—"}
                    </span>
                  </li>
                );
              })}
            </ul>
          </>
        ) : (
          <QueryStateNotice
            query={devices}
            subject={t("upsStatusWidget.upsDevices")}
            empty={
              canAddServer ? (
                <Trans t={t} i18nKey="upsStatusWidget.noUPSMonitoredYetLink" components={{ navlink: <Link to="/power" className="underline" /> }} />
              ) : (
                t("upsStatusWidget.noUPSMonitoredYet")
              )
            }
          />
        )}
      </CardContent>
    </Card>
  );
}
