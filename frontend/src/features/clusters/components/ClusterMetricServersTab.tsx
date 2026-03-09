import { useState } from "react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow,
} from "@/components/ui/table";
import {
  Dialog, DialogContent, DialogHeader, DialogTitle, DialogTrigger,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";
import { Plus, Trash2 } from "lucide-react";
import { useAuth } from "@/hooks/useAuth";
import { useMetricServers, useCreateMetricServer, useDeleteMetricServer } from "../api/metric-server-queries";

interface ClusterMetricServersTabProps {
  clusterId: string;
}

export function ClusterMetricServersTab({ clusterId }: ClusterMetricServersTabProps) {
  const { canManage } = useAuth();
  const serversQuery = useMetricServers(clusterId);
  const createServer = useCreateMetricServer(clusterId);
  const deleteServer = useDeleteMetricServer(clusterId);

  const [open, setOpen] = useState(false);
  const [id, setId] = useState("");
  const [type, setType] = useState("influxdb");
  const [server, setServer] = useState("");
  const [port, setPort] = useState("8089");

  const handleCreate = (e: React.SyntheticEvent) => {
    e.preventDefault();
    createServer.mutate({ id, type, server, port: parseInt(port, 10) }, {
      onSuccess: () => { setOpen(false); setId(""); setServer(""); },
    });
  };

  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between">
        <CardTitle>Metric Servers</CardTitle>
        {canManage("cluster") && (
          <Dialog open={open} onOpenChange={setOpen}>
            <DialogTrigger asChild>
              <Button size="sm"><Plus className="mr-2 h-4 w-4" />Add Server</Button>
            </DialogTrigger>
            <DialogContent className="max-w-sm">
              <DialogHeader><DialogTitle>Add Metric Server</DialogTitle></DialogHeader>
              <form onSubmit={handleCreate} className="space-y-4">
                <div><Label>ID</Label><Input value={id} onChange={(e) => { setId(e.target.value); }} required /></div>
                <div>
                  <Label>Type</Label>
                  <select className="w-full rounded border bg-background px-2 py-2 text-sm" value={type} onChange={(e) => { setType(e.target.value); }}>
                    <option value="influxdb">InfluxDB</option>
                    <option value="graphite">Graphite</option>
                  </select>
                </div>
                <div><Label>Server</Label><Input value={server} onChange={(e) => { setServer(e.target.value); }} required /></div>
                <div><Label>Port</Label><Input value={port} onChange={(e) => { setPort(e.target.value); }} type="number" required /></div>
                <Button type="submit" disabled={createServer.isPending}>{createServer.isPending ? "Creating..." : "Create"}</Button>
              </form>
            </DialogContent>
          </Dialog>
        )}
      </CardHeader>
      <CardContent>
        {serversQuery.isLoading ? <Skeleton className="h-20 w-full" /> :
         !serversQuery.data || serversQuery.data.length === 0 ? (
          <p className="text-sm text-muted-foreground">No external metric servers configured.</p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>ID</TableHead>
                <TableHead>Type</TableHead>
                <TableHead>Server</TableHead>
                <TableHead>Port</TableHead>
                <TableHead>Status</TableHead>
                {canManage("cluster") && <TableHead className="text-right">Actions</TableHead>}
              </TableRow>
            </TableHeader>
            <TableBody>
              {serversQuery.data.map((s) => (
                <TableRow key={s.id}>
                  <TableCell className="font-medium">{s.id}</TableCell>
                  <TableCell><Badge variant="outline">{s.type}</Badge></TableCell>
                  <TableCell>{s.server}</TableCell>
                  <TableCell>{s.port}</TableCell>
                  <TableCell><Badge variant={s.disable ? "secondary" : "default"}>{s.disable ? "Disabled" : "Active"}</Badge></TableCell>
                  {canManage("cluster") && (
                    <TableCell className="text-right">
                      <Button variant="ghost" size="sm" onClick={() => { deleteServer.mutate(s.id); }}>
                        <Trash2 className="h-4 w-4 text-destructive" />
                      </Button>
                    </TableCell>
                  )}
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </CardContent>
    </Card>
  );
}
