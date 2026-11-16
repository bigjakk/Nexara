import { Link } from "react-router-dom";
import { Power } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import { Panel } from "@/components/page/Panel";
import { NodeShutdownConfirm } from "@/features/clusters/components/node/NodeShutdownConfirm";
import { usePermissions } from "@/hooks/usePermissions";
import { cn } from "@/lib/utils";
import { useNutServers, useSetUpsFeedProtectedElsewhere, useUpsDevices } from "../api/ups-queries";
import { feedName, protectionView, upsmonSystem } from "../lib/ups-feeds";
import { formatRuntime, upsOnBattery } from "../lib/ups-state";
import { DANGER_OUTLINE } from "../lib/ups-style";
import type { NutServer, UpsDevice, UpsFeed } from "../types/ups";
import { ProtectionBadge, UpsStatusBadges } from "./UpsBadges";
import { useTranslation, Trans } from "react-i18next";

interface Props {
  clusterId: string;
  nodeName: string;
  online: boolean;
  className?: string;
}

/**
 * The UPSes that power a node and whether it would shut down with them, for
 * view:ups holders. A node that is not protected gets the two lines that make
 * it a upsmon secondary, and during an outage a way to shut it down now.
 */
export function NodePowerCard(props: Props) {
  const { hasGlobalPermission } = usePermissions();
  if (!hasGlobalPermission("view", "ups")) return null;
  return <NodePowerCardBody {...props} />;
}

function NodePowerCardBody({ clusterId, nodeName, online, className }: Props) {
  const { t } = useTranslation("power");
  const devices = useUpsDevices();
  const servers = useNutServers();
  const all = devices.data ?? [];
  // Nothing to say on an install that watches no UPS at all.
  if (all.length === 0) return null;
  const feeding = all
    .map((d) => ({ device: d, feed: d.feeds.find((f) => f.kind === "node" && f.cluster_id === clusterId && f.node_name === nodeName) }))
    .filter((x): x is { device: UpsDevice; feed: UpsFeed } => x.feed !== undefined);
  // A node another UPS, still on mains, also powers does not lose power, as
  // the Power page's outage banner reads it.
  const stillFedBy = feeding.filter(({ device }) => device.state === "online").map(({ device }) => device.name);

  return (
    <Panel title={t("nodePowerCard.power")} className={className}>
      {feeding.length === 0 ? (
        <p className="text-sm text-muted-foreground">
          <Trans t={t} i18nKey="nodePowerCard.noUPSNexaraKnowsPowers" values={{ nodeName }} components={{ navlink: <Link to="/power" className="text-primary hover:underline" /> }} />
        </p>
      ) : (
        // One block per UPS, divided by a rule: no box inside the panel.
        <div className="divide-y text-sm">
          {feeding.map(({ device, feed }) => (
            <FedBy
              key={device.id}
              device={device}
              feed={feed}
              server={servers.data?.find((s) => s.id === device.server_id)}
              clusterId={clusterId}
              nodeName={nodeName}
              online={online}
              stillFedBy={stillFedBy}
            />
          ))}
        </div>
      )}
    </Panel>
  );
}

function FedBy({ device, feed, server, clusterId, nodeName, online, stillFedBy }: {
  device: UpsDevice;
  feed: UpsFeed;
  server: NutServer | undefined;
  clusterId: string;
  nodeName: string;
  online: boolean;
  stillFedBy: string[];
}) {
  const { t } = useTranslation("power");
  const { canManage, hasGlobalPermission } = usePermissions();
  const elsewhere = useSetUpsFeedProtectedElsewhere(device.id);
  const others = device.feeds.filter((f) => f.id !== feed.id).map(feedName);
  const onBattery = upsOnBattery(device);
  const unprotected = feed.protection === "not_protected";
  // The last reading of a UPS Nexara cannot read now is not drawn as live, as on its card.
  const value = cn("font-medium tabular-nums", !(device.reachability === "ok" && !device.reading_stale) && "text-muted-foreground");

  return (
    <div className="space-y-3 py-3.5 first:pt-0 last:pb-0">
      <div className="flex flex-wrap items-center gap-2">
        <span>
          <Trans t={t} i18nKey="nodePowerCard.poweredByLinkNameLink" values={{ name: device.name, serverName: device.server_name }} components={{ navlink: <Link to={`/power/${device.id}`} className="font-medium hover:underline" />, span: <span className="text-muted-foreground" /> }} />
        </span>
        <UpsStatusBadges device={device} />
      </div>
      <dl className="grid grid-cols-3 gap-2">
        <div><dt className="text-xs text-muted-foreground">{t("nodePowerCard.battery")}</dt><dd className={value}>{device.battery_charge == null ? "—" : `${String(Math.round(device.battery_charge))}%`}</dd></div>
        <div><dt className="text-xs text-muted-foreground">{t("nodePowerCard.runtime")}</dt><dd className={value}>{formatRuntime(device.battery_runtime)}</dd></div>
        <div><dt className="text-xs text-muted-foreground">{t("nodePowerCard.load")}</dt><dd className={value}>{device.load == null ? "—" : `${String(Math.round(device.load))}%`}</dd></div>
      </dl>
      {others.length > 0 && <p className="text-xs text-muted-foreground">{t("nodePowerCard.alsoPowersOthers", { others: others.join(", ") })}</p>}

      <div className="space-y-2">
        <div className="flex flex-wrap items-center gap-2">
          <span className="text-xs font-medium">{t("nodePowerCard.shutdown")}</span>
          <ProtectionBadge feed={feed} />
          <span className="text-xs text-muted-foreground">{protectionView(feed).hint}</span>
        </div>
        {hasGlobalPermission("manage", "ups") && (
          <div className="flex items-center gap-2 text-xs text-muted-foreground">
            <Switch
              checked={feed.protected_elsewhere}
              aria-label={t("nodePowerCard.nodeNameShutsDownAnotherWay", { nodeName, name: device.name })}
              disabled={elsewhere.isPending}
              onCheckedChange={(on) => { elsewhere.mutate({ feedId: feed.id, protectedElsewhere: on }); }}
            />
            <span>
              {t("nodePowerCard.protectedAnotherWayNodeNameShuts", { nodeName })}
            </span>
          </div>
        )}
        {unprotected && (
          <div className="space-y-1.5 text-xs">
            <p className="text-muted-foreground">
              {t("nodePowerCard.toShutNodeNameDownBefore", { nodeName, name: device.name, serverName: device.server_name })}
            </p>
            <pre className="overflow-x-auto rounded-md bg-foreground/[0.06] p-2.5 font-mono">
              {`# /etc/nut/nut.conf\nMODE=netclient\n\n# /etc/nut/upsmon.conf\nMONITOR ${upsmonSystem(device.name, server)} 1 <user> <password> secondary`}
            </pre>
          </div>
        )}
        {unprotected && onBattery && online && stillFedBy.length > 0 && (
          <p className="text-xs text-muted-foreground">
            {t("nodePowerCard.nodeNameKeepsPowerForNow", { nodeName, stillFedBy: stillFedBy.join(", ") })}
          </p>
        )}
        {unprotected && onBattery && online && stillFedBy.length === 0 && canManage("node") && (
          <NodeShutdownConfirm
            clusterId={clusterId}
            nodeName={nodeName}
            trigger={
              <Button size="sm" variant="outline" className={cn("gap-1.5", DANGER_OUTLINE)}>
                <Power className="h-4 w-4" aria-hidden="true" />
                {t("nodePowerCard.shutDownNodeNameNow", { nodeName })}
              </Button>
            }
          />
        )}
      </div>
    </div>
  );
}
