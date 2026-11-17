import { useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Panel } from "@/components/page/Panel";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Plus, Trash2 } from "lucide-react";
import { QueryStateNotice } from "@/components/QueryStateNotice";
import { useAuth } from "@/hooks/useAuth";
import { ConfirmDeleteDialog } from "@/components/ConfirmDeleteDialog";
import { ClusterHistoryButton } from "@/features/history/components/ClusterHistoryButton";
import { clusterHistories } from "@/features/history/lib/cluster-histories";
import {
  type MetricServerConfig,
  useMetricServers,
  useCreateMetricServer,
  useDeleteMetricServer,
} from "../api/metric-server-queries";

interface ClusterMetricServersTabProps {
  clusterId: string;
}

export function ClusterMetricServersTab({
  clusterId,
}: ClusterMetricServersTabProps) {
  const { canManage } = useAuth();
  const serversQuery = useMetricServers(clusterId);
  const createServer = useCreateMetricServer(clusterId);
  const deleteServer = useDeleteMetricServer(clusterId);

  const [open, setOpen] = useState(false);
  const [pendingDelete, setPendingDelete] = useState<MetricServerConfig | null>(
    null,
  );
  const [id, setId] = useState("");
  const [type, setType] = useState("influxdb");
  const [server, setServer] = useState("");
  const [port, setPort] = useState("8089");

  const handleCreate = (e: React.SyntheticEvent) => {
    e.preventDefault();
    createServer.mutate(
      { id, type, server, port: parseInt(port, 10) },
      {
        onSuccess: () => {
          setOpen(false);
          setId("");
          setServer("");
        },
      },
    );
  };

  const servers = serversQuery.data ?? [];
  const disabledCount = servers.filter((sv) => sv.disable).length;

  return (
    <Panel
      flush
      title="Metric servers"
      aside={
        <span className="flex flex-wrap items-center gap-x-2">
          <ClusterHistoryButton
            history={clusterHistories.metrics(clusterId)}
            title="metric servers"
          />
          {servers.length === 0
            ? "Where Proxmox sends this cluster's metrics, besides Nexara"
            : `${String(servers.length)} ${servers.length === 1 ? "server" : "servers"}${disabledCount > 0 ? `, ${String(disabledCount)} disabled` : ""}`}
        </span>
      }
      actions={
        canManage("cluster") ? (
          <Dialog open={open} onOpenChange={setOpen}>
            <DialogTrigger asChild>
              <Button size="sm">
                <Plus className="h-4 w-4" />
                Add server
              </Button>
            </DialogTrigger>
            <DialogContent className="max-w-sm">
              <DialogHeader>
                <DialogTitle>Add metric server</DialogTitle>
              </DialogHeader>
              <form onSubmit={handleCreate} className="space-y-4">
                <div className="space-y-2">
                  <Label htmlFor="metric-server-id">ID</Label>
                  <Input
                    id="metric-server-id"
                    value={id}
                    onChange={(e) => {
                      setId(e.target.value);
                    }}
                    required
                  />
                </div>
                <div className="space-y-2">
                  <Label htmlFor="metric-server-type">Type</Label>
                  <select
                    id="metric-server-type"
                    className="flex h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-xs outline-hidden focus-visible:ring-1 focus-visible:ring-ring"
                    value={type}
                    onChange={(e) => {
                      setType(e.target.value);
                    }}
                  >
                    <option value="influxdb">InfluxDB</option>
                    <option value="graphite">Graphite</option>
                  </select>
                </div>
                <div className="space-y-2">
                  <Label htmlFor="metric-server-host">Server</Label>
                  <Input
                    id="metric-server-host"
                    value={server}
                    onChange={(e) => {
                      setServer(e.target.value);
                    }}
                    required
                  />
                </div>
                <div className="space-y-2">
                  <Label htmlFor="metric-server-port">Port</Label>
                  <Input
                    id="metric-server-port"
                    value={port}
                    onChange={(e) => {
                      setPort(e.target.value);
                    }}
                    type="number"
                    required
                  />
                </div>
                <DialogFooter>
                  <Button type="submit" disabled={createServer.isPending}>
                    {createServer.isPending ? "Creating..." : "Create"}
                  </Button>
                </DialogFooter>
              </form>
            </DialogContent>
          </Dialog>
        ) : undefined
      }
    >
      {servers.length === 0 ? (
        <div className="px-[18px] pb-1">
          <QueryStateNotice
            query={serversQuery}
            subject="this cluster's metric servers"
            empty="No external metric servers configured."
            identity={clusterId}
            skeletonClassName="h-20 w-full"
          />
        </div>
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>ID</TableHead>
              <TableHead>Type</TableHead>
              <TableHead>Server</TableHead>
              <TableHead>Port</TableHead>
              <TableHead>State</TableHead>
              {canManage("cluster") && (
                <TableHead className="w-0">
                  <span className="sr-only">Actions</span>
                </TableHead>
              )}
            </TableRow>
          </TableHeader>
          <TableBody>
            {servers.map((s) => (
              <TableRow key={s.id}>
                <TableCell className="font-medium">
                  {s.id}
                  <ClusterHistoryButton
                    history={clusterHistories.metricServer(clusterId, s.id)}
                    title={s.id}
                  />
                </TableCell>
                <TableCell>{s.type}</TableCell>
                <TableCell className="font-mono text-[12.5px]">
                  {s.server}
                </TableCell>
                <TableCell className="font-mono text-[12.5px] tabular-nums">
                  {s.port}
                </TableCell>
                <TableCell>
                  <Badge variant={s.disable ? "secondary" : "healthy"}>
                    {s.disable ? "Disabled" : "Active"}
                  </Badge>
                </TableCell>
                {canManage("cluster") && (
                  <TableCell className="py-1 text-right">
                    <Button
                      aria-label={`Delete ${s.id}`}
                      variant="ghost"
                      size="icon"
                      className="h-8 w-8 text-muted-foreground hover:text-destructive"
                      onClick={() => {
                        setPendingDelete(s);
                      }}
                    >
                      <Trash2 className="h-4 w-4" />
                    </Button>
                  </TableCell>
                )}
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
      {/*
        pve-manager PVE/API2/Cluster/MetricServer.pm "delete" drops the entry
        from status.cfg after the plugin's on_delete_hook, which for InfluxDB
        (PVE/Status/InfluxDB.pm) unlinks the token file stored for it. Nothing
        is sent to the metric server itself.
      */}
      <ConfirmDeleteDialog
        target={pendingDelete}
        onClose={() => {
          setPendingDelete(null);
        }}
        onConfirm={(s) => {
          deleteServer.mutate(s.id);
        }}
        title={(s) => `Delete metric server ${s.id}?`}
        description={(s) =>
          `${
            s.disable
              ? `Proxmox deletes this metric server as soon as you confirm. It is disabled, so no metrics are being sent to ${s.server}:${String(s.port)} now.`
              : `Proxmox deletes this metric server as soon as you confirm and stops sending the cluster's metrics to ${s.server}:${String(s.port)}.`
          } Its settings are gone, including any InfluxDB token Proxmox stored for it. Metrics already sent stay on ${s.server}. To undo, add the server again.`
        }
      />
    </Panel>
  );
}
