import { Link } from "react-router-dom";
import { BatteryCharging, Power } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Switch } from "@/components/ui/switch";
import { NodeShutdownConfirm } from "@/features/clusters/components/node/NodeShutdownConfirm";
import { usePermissions } from "@/hooks/usePermissions";
import { cn } from "@/lib/utils";
import { useNutServers, useSetUpsFeedProtectedElsewhere, useUpsDevices } from "../api/ups-queries";
import { feedName, protectionView, upsmonSystem } from "../lib/ups-feeds";
import { formatRuntime, upsOnBattery } from "../lib/ups-state";
import type { NutServer, UpsDevice, UpsFeed } from "../types/ups";
import { UpsStatusBadges } from "./UpsBadges";
import { ProtectionBadge } from "./UpsPowers";

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
    <Card className={className}>
      <CardHeader className="pb-2">
        <CardTitle className="flex items-center gap-2 text-sm font-medium">
          <BatteryCharging className="h-4 w-4 text-muted-foreground" aria-hidden="true" />
          Power
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-4 text-sm">
        {feeding.length === 0 ? (
          <p className="text-muted-foreground">
            No UPS Nexara knows powers {nodeName}. Add it to a UPS&apos;s Powers tab on the{" "}
            <Link to="/power" className="text-primary hover:underline">Power page</Link> to have it checked.
          </p>
        ) : (
          feeding.map(({ device, feed }) => (
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
          ))
        )}
      </CardContent>
    </Card>
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
  const { canManage, hasGlobalPermission } = usePermissions();
  const elsewhere = useSetUpsFeedProtectedElsewhere(device.id);
  const others = device.feeds.filter((f) => f.id !== feed.id).map(feedName);
  const onBattery = upsOnBattery(device);
  const unprotected = feed.protection === "not_protected";
  // The last reading of a UPS Nexara cannot read now is not drawn as live, as on its card.
  const value = cn("font-medium tabular-nums", !(device.reachability === "ok" && !device.reading_stale) && "text-muted-foreground");

  return (
    <div className="space-y-3 rounded-md border p-3">
      <div className="flex flex-wrap items-center gap-2">
        <span>
          Powered by{" "}
          <Link to={`/power/${device.id}`} className="font-medium hover:underline">{device.name}</Link>
          <span className="text-muted-foreground"> via {device.server_name}</span>
        </span>
        <UpsStatusBadges device={device} />
      </div>
      <dl className="grid grid-cols-3 gap-2 text-xs">
        <div><dt className="text-muted-foreground">Battery</dt><dd className={value}>{device.battery_charge == null ? "—" : `${String(Math.round(device.battery_charge))}%`}</dd></div>
        <div><dt className="text-muted-foreground">Runtime</dt><dd className={value}>{formatRuntime(device.battery_runtime)}</dd></div>
        <div><dt className="text-muted-foreground">Load</dt><dd className={value}>{device.load == null ? "—" : `${String(Math.round(device.load))}%`}</dd></div>
      </dl>
      {others.length > 0 && <p className="text-xs text-muted-foreground">Also powers {others.join(", ")}</p>}

      <div className="space-y-2">
        <div className="flex flex-wrap items-center gap-2">
          <span className="text-xs font-medium">Shutdown</span>
          <ProtectionBadge feed={feed} />
          <span className="text-xs text-muted-foreground">{protectionView(feed).hint}</span>
        </div>
        {hasGlobalPermission("manage", "ups") && (
          <div className="flex items-center gap-2 text-xs text-muted-foreground">
            <Switch
              checked={feed.protected_elsewhere}
              aria-label={`${nodeName} shuts down another way when ${device.name} runs out`}
              disabled={elsewhere.isPending}
              onCheckedChange={(on) => { elsewhere.mutate({ feedId: feed.id, protectedElsewhere: on }); }}
            />
            <span>
              Protected another way: {nodeName} shuts down some way Nexara cannot see, such as a upsmon behind NAT or a
              proxy, or another tool, so it is not warned about.
            </span>
          </div>
        )}
        {unprotected && (
          <div className="space-y-1.5 text-xs">
            <p className="text-muted-foreground">
              To shut {nodeName} down before {device.name}&apos;s battery runs out, run upsmon on it as a secondary of{" "}
              {device.server_name}, with an account from its upsd.users:
            </p>
            <pre className="overflow-x-auto rounded bg-muted p-2 font-mono">
              {`# /etc/nut/nut.conf\nMODE=netclient\n\n# /etc/nut/upsmon.conf\nMONITOR ${upsmonSystem(device.name, server)} 1 <user> <password> secondary`}
            </pre>
          </div>
        )}
        {unprotected && onBattery && online && stillFedBy.length > 0 && (
          <p className="text-xs text-muted-foreground">
            {nodeName} keeps power for now: {stillFedBy.join(", ")} also powers it and is on mains.
          </p>
        )}
        {unprotected && onBattery && online && stillFedBy.length === 0 && canManage("node") && (
          <NodeShutdownConfirm
            clusterId={clusterId}
            nodeName={nodeName}
            trigger={
              <Button size="sm" variant="destructive" className="gap-1.5">
                <Power className="h-4 w-4" aria-hidden="true" />
                Shut down {nodeName} now…
              </Button>
            }
          />
        )}
      </div>
    </div>
  );
}
