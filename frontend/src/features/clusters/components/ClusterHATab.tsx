import { useState } from "react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
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
import {
  useHAResources, useHAGroups, useHAStatus,
  useCreateHAResource, useDeleteHAResource,
  useCreateHAGroup, useDeleteHAGroup,
} from "@/features/ha/api/ha-queries";

interface ClusterHATabProps {
  clusterId: string;
}

export function ClusterHATab({ clusterId }: ClusterHATabProps) {
  const { canManage } = useAuth();
  const resourcesQuery = useHAResources(clusterId);
  const groupsQuery = useHAGroups(clusterId);
  const statusQuery = useHAStatus(clusterId);
  const createResource = useCreateHAResource(clusterId);
  const deleteResource = useDeleteHAResource(clusterId);
  const createGroup = useCreateHAGroup(clusterId);
  const deleteGroup = useDeleteHAGroup(clusterId);

  const [resOpen, setResOpen] = useState(false);
  const [resSID, setResSID] = useState("");
  const [resState, setResState] = useState("started");

  const [grpOpen, setGrpOpen] = useState(false);
  const [grpName, setGrpName] = useState("");
  const [grpNodes, setGrpNodes] = useState("");

  const handleCreateResource = (e: React.SyntheticEvent) => {
    e.preventDefault();
    createResource.mutate({ sid: resSID, state: resState }, {
      onSuccess: () => { setResOpen(false); setResSID(""); },
    });
  };

  const handleCreateGroup = (e: React.SyntheticEvent) => {
    e.preventDefault();
    createGroup.mutate({ group: grpName, nodes: grpNodes }, {
      onSuccess: () => { setGrpOpen(false); setGrpName(""); setGrpNodes(""); },
    });
  };

  return (
    <Tabs defaultValue="status">
      <TabsList>
        <TabsTrigger value="status">Status</TabsTrigger>
        <TabsTrigger value="resources">Resources</TabsTrigger>
        <TabsTrigger value="groups">Groups</TabsTrigger>
      </TabsList>

      <TabsContent value="status" className="mt-4">
        <Card>
          <CardHeader><CardTitle>HA Status</CardTitle></CardHeader>
          <CardContent>
            {statusQuery.isLoading ? <Skeleton className="h-20 w-full" /> :
             !statusQuery.data || statusQuery.data.length === 0 ? (
              <p className="text-sm text-muted-foreground">No HA status available.</p>
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>ID</TableHead>
                    <TableHead>Type</TableHead>
                    <TableHead>Node</TableHead>
                    <TableHead>Status</TableHead>
                    <TableHead>State</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {statusQuery.data.map((entry) => (
                    <TableRow key={entry.id}>
                      <TableCell className="font-mono text-xs">{entry.id}</TableCell>
                      <TableCell>{entry.type}</TableCell>
                      <TableCell>{entry.node}</TableCell>
                      <TableCell><Badge variant={entry.status === "active" ? "default" : "secondary"}>{entry.status}</Badge></TableCell>
                      <TableCell>{entry.state ?? entry.crm_state}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
          </CardContent>
        </Card>
      </TabsContent>

      <TabsContent value="resources" className="mt-4">
        <Card>
          <CardHeader className="flex flex-row items-center justify-between">
            <CardTitle>HA Resources</CardTitle>
            {canManage("ha") && (
              <Dialog open={resOpen} onOpenChange={setResOpen}>
                <DialogTrigger asChild>
                  <Button size="sm"><Plus className="mr-2 h-4 w-4" />Add Resource</Button>
                </DialogTrigger>
                <DialogContent className="max-w-sm">
                  <DialogHeader><DialogTitle>Add HA Resource</DialogTitle></DialogHeader>
                  <form onSubmit={handleCreateResource} className="space-y-4">
                    <div><Label>SID (e.g. vm:100)</Label><Input value={resSID} onChange={(e) => { setResSID(e.target.value); }} required /></div>
                    <div><Label>State</Label><Input value={resState} onChange={(e) => { setResState(e.target.value); }} /></div>
                    <Button type="submit" disabled={createResource.isPending}>{createResource.isPending ? "Creating..." : "Create"}</Button>
                  </form>
                </DialogContent>
              </Dialog>
            )}
          </CardHeader>
          <CardContent>
            {resourcesQuery.isLoading ? <Skeleton className="h-20 w-full" /> :
             !resourcesQuery.data || resourcesQuery.data.length === 0 ? (
              <p className="text-sm text-muted-foreground">No HA resources configured.</p>
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>SID</TableHead>
                    <TableHead>State</TableHead>
                    <TableHead>Group</TableHead>
                    <TableHead>Max Relocate</TableHead>
                    {canManage("ha") && <TableHead className="text-right">Actions</TableHead>}
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {resourcesQuery.data.map((res) => (
                    <TableRow key={res.sid}>
                      <TableCell className="font-medium">{res.sid}</TableCell>
                      <TableCell><Badge variant="secondary">{res.state}</Badge></TableCell>
                      <TableCell>{res.group || "—"}</TableCell>
                      <TableCell>{res.max_relocate}</TableCell>
                      {canManage("ha") && (
                        <TableCell className="text-right">
                          <Button variant="ghost" size="sm" onClick={() => { deleteResource.mutate(res.sid); }}>
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
      </TabsContent>

      <TabsContent value="groups" className="mt-4">
        <Card>
          <CardHeader className="flex flex-row items-center justify-between">
            <CardTitle>HA Groups</CardTitle>
            {canManage("ha") && (
              <Dialog open={grpOpen} onOpenChange={setGrpOpen}>
                <DialogTrigger asChild>
                  <Button size="sm"><Plus className="mr-2 h-4 w-4" />Add Group</Button>
                </DialogTrigger>
                <DialogContent className="max-w-sm">
                  <DialogHeader><DialogTitle>Create HA Group</DialogTitle></DialogHeader>
                  <form onSubmit={handleCreateGroup} className="space-y-4">
                    <div><Label>Group Name</Label><Input value={grpName} onChange={(e) => { setGrpName(e.target.value); }} required /></div>
                    <div><Label>Nodes (e.g. node1:100,node2:50)</Label><Input value={grpNodes} onChange={(e) => { setGrpNodes(e.target.value); }} required /></div>
                    <Button type="submit" disabled={createGroup.isPending}>{createGroup.isPending ? "Creating..." : "Create"}</Button>
                  </form>
                </DialogContent>
              </Dialog>
            )}
          </CardHeader>
          <CardContent>
            {groupsQuery.isLoading ? <Skeleton className="h-20 w-full" /> :
             !groupsQuery.data || groupsQuery.data.length === 0 ? (
              <p className="text-sm text-muted-foreground">No HA groups configured.</p>
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Group</TableHead>
                    <TableHead>Nodes</TableHead>
                    <TableHead>Restricted</TableHead>
                    <TableHead>No Failback</TableHead>
                    {canManage("ha") && <TableHead className="text-right">Actions</TableHead>}
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {groupsQuery.data.map((g) => (
                    <TableRow key={g.group}>
                      <TableCell className="font-medium">{g.group}</TableCell>
                      <TableCell className="text-xs">{g.nodes}</TableCell>
                      <TableCell>{g.restricted ? "Yes" : "No"}</TableCell>
                      <TableCell>{g.nofailback ? "Yes" : "No"}</TableCell>
                      {canManage("ha") && (
                        <TableCell className="text-right">
                          <Button variant="ghost" size="sm" onClick={() => { deleteGroup.mutate(g.group); }}>
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
      </TabsContent>
    </Tabs>
  );
}
