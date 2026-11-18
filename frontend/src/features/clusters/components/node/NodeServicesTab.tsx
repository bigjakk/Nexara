import { useState } from "react";
import { Play, RotateCw, Square } from "lucide-react";

import { ConfirmDeleteDialog } from "@/components/ConfirmDeleteDialog";
import { Panel } from "@/components/page/Panel";
import { QueryStateNotice } from "@/components/QueryStateNotice";
import { Button } from "@/components/ui/button";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { usePermissions } from "@/hooks/usePermissions";
import { useNodeServices, useServiceAction } from "../../api/cluster-queries";
import { NoValue, StateText, type StateTone } from "./StateText";

// Proxmox refuses to stop these: the stop task dies with "refusing to stop
// essential service!" (pve-manager PVE/API2/Services.pm $essential_services).
// They can still be restarted.
const UNSTOPPABLE_SERVICES = new Set(["pveproxy", "pvedaemon", "pve-cluster"]);

/** Running is fine, failed is a fault, and anything else is simply not running. */
function serviceTone(state: string): StateTone {
  if (state === "running") return "ok";
  return state === "failed" ? "bad" : "off";
}

export function ServicesTab({
  clusterId,
  nodeName,
}: {
  clusterId: string;
  nodeName: string;
}) {
  const servicesQuery = useNodeServices(clusterId, nodeName);
  const services = servicesQuery.data;
  const serviceAction = useServiceAction(clusterId, nodeName);
  // Start, Restart and Stop take manage:node; without it the list is read-only.
  const canAct = usePermissions().canManage("node");
  // Restart and Stop only set this and send from its confirmation; Start
  // sends at once.
  const [pendingAction, setPendingAction] = useState<{
    service: string;
    action: "restart" | "stop";
  } | null>(null);
  const count = services?.length ?? 0;
  const running = services?.filter((s) => s.state === "running").length ?? 0;

  // The panel and its title are outside the branch so they stay whatever the
  // read does: a failure then reads as this section failing rather than as the
  // section not existing.
  return (
    <>
      <Panel
        flush
        title="Services"
        aside={
          count > 0
            ? running === count
              ? `${String(count)} services, all running`
              : `${String(running)} of ${String(count)} running`
            : undefined
        }
      >
        {!services || services.length === 0 ? (
          <div className="px-[18px] pb-1">
            <QueryStateNotice
              query={servicesQuery}
              subject="this node's services"
              empty="No services found."
              skeletonClassName="h-48 w-full"
            />
          </div>
        ) : (
          <Table className="whitespace-nowrap">
            <TableHeader>
              <TableRow>
                <TableHead>Service</TableHead>
                <TableHead>Description</TableHead>
                <TableHead>State</TableHead>
                {canAct && <TableHead className="w-44" />}
              </TableRow>
            </TableHeader>
            <TableBody>
              {services.map((svc) => (
                <TableRow key={svc.service}>
                  <TableCell className="font-mono text-[13px]">
                    {svc.service}
                  </TableCell>
                  <TableCell className="min-w-[14rem] whitespace-normal text-muted-foreground">
                    {svc.desc || svc.name || <NoValue />}
                  </TableCell>
                  <TableCell>
                    <StateText tone={serviceTone(svc.state)}>
                      {svc.state}
                    </StateText>
                  </TableCell>
                  {canAct && (
                    <TableCell className="py-1.5">
                      <div className="flex justify-end gap-1">
                        {svc.state !== "running" && (
                          <Button
                            variant="ghost"
                            size="sm"
                            className="h-7 gap-1.5 px-2"
                            onClick={() => {
                              serviceAction.mutate({
                                service: svc.service,
                                action: "start",
                              });
                            }}
                            disabled={serviceAction.isPending}
                          >
                            <Play className="h-3.5 w-3.5" />
                            Start
                          </Button>
                        )}
                        {svc.state === "running" && (
                          <>
                            <Button
                              variant="ghost"
                              size="sm"
                              className="h-7 gap-1.5 px-2"
                              onClick={() => {
                                setPendingAction({
                                  service: svc.service,
                                  action: "restart",
                                });
                              }}
                              disabled={serviceAction.isPending}
                            >
                              <RotateCw className="h-3.5 w-3.5" />
                              Restart
                            </Button>
                            {!UNSTOPPABLE_SERVICES.has(svc.service) && (
                              <Button
                                variant="ghost"
                                size="sm"
                                className="h-7 gap-1.5 px-2"
                                onClick={() => {
                                  setPendingAction({
                                    service: svc.service,
                                    action: "stop",
                                  });
                                }}
                                disabled={serviceAction.isPending}
                              >
                                <Square className="h-3.5 w-3.5" />
                                Stop
                              </Button>
                            )}
                          </>
                        )}
                      </div>
                    </TableCell>
                  )}
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </Panel>

      <ConfirmDeleteDialog
        target={pendingAction}
        onClose={() => {
          setPendingAction(null);
        }}
        onConfirm={(p) => {
          serviceAction.mutate(p);
        }}
        title={(p) =>
          `${p.action === "stop" ? "Stop" : "Restart"} ${p.service} on ${nodeName}?`
        }
        description={(p) =>
          p.action === "stop"
            ? `Proxmox stops ${p.service} on ${nodeName} as soon as you confirm, and it stays stopped until it is started again. Whatever relies on it stops working until then.`
            : `Proxmox restarts ${p.service} on ${nodeName} as soon as you confirm. Whatever relies on it is interrupted while it restarts.`
        }
        confirmLabel={(p) => (p.action === "stop" ? "Stop" : "Restart")}
      />
    </>
  );
}
