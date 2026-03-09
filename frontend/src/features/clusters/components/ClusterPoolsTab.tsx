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
import { ChevronDown, ChevronRight, Plus, Trash2 } from "lucide-react";
import { useAuth } from "@/hooks/useAuth";
import { useResourcePools, useResourcePool, useCreatePool, useDeletePool } from "@/features/pools/api/pool-queries";
import { formatBytes } from "@/lib/format";

interface ClusterPoolsTabProps {
  clusterId: string;
}

export function ClusterPoolsTab({ clusterId }: ClusterPoolsTabProps) {
  const { canManage } = useAuth();
  const poolsQuery = useResourcePools(clusterId);
  const createPool = useCreatePool(clusterId);
  const deletePool = useDeletePool(clusterId);

  const [open, setOpen] = useState(false);
  const [poolId, setPoolId] = useState("");
  const [comment, setComment] = useState("");
  const [expanded, setExpanded] = useState<string | null>(null);

  const handleCreate = (e: React.SyntheticEvent) => {
    e.preventDefault();
    const data: { poolid: string; comment?: string } = { poolid: poolId };
    if (comment) data.comment = comment;
    createPool.mutate(data, {
      onSuccess: () => { setOpen(false); setPoolId(""); setComment(""); },
    });
  };

  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between">
        <CardTitle>Resource Pools</CardTitle>
        {canManage("pool") && (
          <Dialog open={open} onOpenChange={setOpen}>
            <DialogTrigger asChild>
              <Button size="sm"><Plus className="mr-2 h-4 w-4" />Create Pool</Button>
            </DialogTrigger>
            <DialogContent className="max-w-sm">
              <DialogHeader><DialogTitle>Create Resource Pool</DialogTitle></DialogHeader>
              <form onSubmit={handleCreate} className="space-y-4">
                <div><Label>Pool ID</Label><Input value={poolId} onChange={(e) => { setPoolId(e.target.value); }} required /></div>
                <div><Label>Comment</Label><Input value={comment} onChange={(e) => { setComment(e.target.value); }} /></div>
                <Button type="submit" disabled={createPool.isPending}>{createPool.isPending ? "Creating..." : "Create"}</Button>
              </form>
            </DialogContent>
          </Dialog>
        )}
      </CardHeader>
      <CardContent>
        {poolsQuery.isLoading ? <Skeleton className="h-20 w-full" /> :
         !poolsQuery.data || poolsQuery.data.length === 0 ? (
          <p className="text-sm text-muted-foreground">No resource pools configured.</p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="w-8"></TableHead>
                <TableHead>Pool ID</TableHead>
                <TableHead>Comment</TableHead>
                {canManage("pool") && <TableHead className="text-right">Actions</TableHead>}
              </TableRow>
            </TableHeader>
            <TableBody>
              {poolsQuery.data.map((pool) => (
                <>
                  <TableRow key={pool.poolid} className="cursor-pointer" onClick={() => { setExpanded(expanded === pool.poolid ? null : pool.poolid); }}>
                    <TableCell>{expanded === pool.poolid ? <ChevronDown className="h-4 w-4" /> : <ChevronRight className="h-4 w-4" />}</TableCell>
                    <TableCell className="font-medium">{pool.poolid}</TableCell>
                    <TableCell className="text-muted-foreground">{pool.comment || "—"}</TableCell>
                    {canManage("pool") && (
                      <TableCell className="text-right">
                        <Button variant="ghost" size="sm" onClick={(e) => { e.stopPropagation(); deletePool.mutate(pool.poolid); }}>
                          <Trash2 className="h-4 w-4 text-destructive" />
                        </Button>
                      </TableCell>
                    )}
                  </TableRow>
                  {expanded === pool.poolid && (
                    <TableRow>
                      <TableCell colSpan={canManage("pool") ? 4 : 3}>
                        <PoolMembers clusterId={clusterId} poolId={pool.poolid} />
                      </TableCell>
                    </TableRow>
                  )}
                </>
              ))}
            </TableBody>
          </Table>
        )}
      </CardContent>
    </Card>
  );
}

function PoolMembers({ clusterId, poolId }: { clusterId: string; poolId: string }) {
  const poolQuery = useResourcePool(clusterId, poolId);

  if (poolQuery.isLoading) return <Skeleton className="h-10 w-full" />;
  if (!poolQuery.data?.members || poolQuery.data.members.length === 0) {
    return <p className="py-2 text-xs text-muted-foreground">No members in this pool.</p>;
  }

  return (
    <div className="py-2">
      <p className="mb-2 text-xs font-medium">Members ({poolQuery.data.members.length})</p>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>ID</TableHead>
            <TableHead>Type</TableHead>
            <TableHead>Name</TableHead>
            <TableHead>Node</TableHead>
            <TableHead>Status</TableHead>
            <TableHead>Memory</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {poolQuery.data.members.map((m) => (
            <TableRow key={m.id}>
              <TableCell className="font-mono text-xs">{m.id}</TableCell>
              <TableCell><Badge variant="outline">{m.type}</Badge></TableCell>
              <TableCell>{m.name ?? "—"}</TableCell>
              <TableCell>{m.node}</TableCell>
              <TableCell><Badge variant={m.status === "running" ? "default" : "secondary"}>{m.status ?? "—"}</Badge></TableCell>
              <TableCell>{m["maxmem"] != null ? formatBytes(Number(m["maxmem"])) : "—"}</TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  );
}
