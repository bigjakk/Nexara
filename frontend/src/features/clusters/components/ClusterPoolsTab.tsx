import { useState, useMemo } from "react";
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
import { Check, ChevronDown, ChevronRight, ChevronsUpDown, Database, Monitor, Pencil, Plus, Trash2, X } from "lucide-react";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Command, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList } from "@/components/ui/command";
import { cn } from "@/lib/utils";
import { useAuth } from "@/hooks/useAuth";
import {
  useResourcePools, useResourcePool, useCreatePool, useUpdatePool, useDeletePool,
} from "@/features/pools/api/pool-queries";
import type { PoolMember } from "@/features/pools/api/pool-queries";
import { useClusterVMs, useClusterStorage } from "@/features/clusters/api/cluster-queries";
import { formatBytes } from "@/lib/format";

interface ClusterPoolsTabProps {
  clusterId: string;
}

export function ClusterPoolsTab({ clusterId }: ClusterPoolsTabProps) {
  const { canManage } = useAuth();
  const poolsQuery = useResourcePools(clusterId);
  const createPool = useCreatePool(clusterId);
  const deletePool = useDeletePool(clusterId);

  const [createOpen, setCreateOpen] = useState(false);
  const [poolId, setPoolId] = useState("");
  const [comment, setComment] = useState("");
  const [expanded, setExpanded] = useState<string | null>(null);
  const [editPoolId, setEditPoolId] = useState<string | null>(null);

  const handleCreate = (e: React.SyntheticEvent) => {
    e.preventDefault();
    const data: { poolid: string; comment?: string } = { poolid: poolId };
    if (comment) data.comment = comment;
    createPool.mutate(data, {
      onSuccess: () => { setCreateOpen(false); setPoolId(""); setComment(""); },
    });
  };

  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between">
        <CardTitle>Resource Pools</CardTitle>
        {canManage("pool") && (
          <Dialog open={createOpen} onOpenChange={setCreateOpen}>
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
                        <Button variant="ghost" size="sm" onClick={(e) => { e.stopPropagation(); setEditPoolId(pool.poolid); }}>
                          <Pencil className="h-4 w-4" />
                        </Button>
                        <Button variant="ghost" size="sm" onClick={(e) => { e.stopPropagation(); deletePool.mutate(pool.poolid); }}>
                          <Trash2 className="h-4 w-4 text-destructive" />
                        </Button>
                      </TableCell>
                    )}
                  </TableRow>
                  {expanded === pool.poolid && (
                    <TableRow>
                      <TableCell colSpan={canManage("pool") ? 4 : 3}>
                        <PoolMembers clusterId={clusterId} poolId={pool.poolid} canManage={canManage("pool")} />
                      </TableCell>
                    </TableRow>
                  )}
                </>
              ))}
            </TableBody>
          </Table>
        )}
      </CardContent>

      {/* Edit Comment Dialog */}
      {editPoolId != null && (
        <EditCommentDialog clusterId={clusterId} poolId={editPoolId} onClose={() => { setEditPoolId(null); }} />
      )}
    </Card>
  );
}

// --- Edit Comment Dialog (pencil button) ---

function EditCommentDialog({ clusterId, poolId, onClose }: { clusterId: string; poolId: string; onClose: () => void }) {
  const poolQuery = useResourcePool(clusterId, poolId);
  const updatePool = useUpdatePool(clusterId);
  const [editComment, setEditComment] = useState<string | null>(null);

  const currentComment = editComment ?? poolQuery.data?.comment ?? "";

  const handleSave = (e: React.SyntheticEvent) => {
    e.preventDefault();
    updatePool.mutate({ poolid: poolId, comment: currentComment }, { onSuccess: onClose });
  };

  return (
    <Dialog open onOpenChange={(v) => { if (!v) onClose(); }}>
      <DialogContent className="max-w-sm">
        <DialogHeader><DialogTitle>Edit Pool: {poolId}</DialogTitle></DialogHeader>
        {poolQuery.isLoading ? <Skeleton className="h-20 w-full" /> : (
          <form onSubmit={handleSave} className="space-y-4">
            <div className="space-y-2">
              <Label>Comment</Label>
              <Input value={currentComment} onChange={(e) => { setEditComment(e.target.value); }} placeholder="Pool description" />
            </div>
            <div className="flex gap-2">
              <Button type="submit" disabled={updatePool.isPending}>
                {updatePool.isPending ? "Saving..." : "Save"}
              </Button>
              <Button type="button" variant="outline" onClick={onClose}>Cancel</Button>
            </div>
          </form>
        )}
      </DialogContent>
    </Dialog>
  );
}

// --- Add VM Dialog ---

function AddVMDialog({ clusterId, poolId, memberVMIDs }: { clusterId: string; poolId: string; memberVMIDs: Set<number> }) {
  const vmsQuery = useClusterVMs(clusterId);
  const updatePool = useUpdatePool(clusterId);
  const [open, setOpen] = useState(false);
  const [selected, setSelected] = useState<number[]>([]);
  const [pickerOpen, setPickerOpen] = useState(false);

  const availableVMs = useMemo(
    () => (vmsQuery.data ?? []).filter((vm) => !memberVMIDs.has(vm.vmid)),
    [vmsQuery.data, memberVMIDs],
  );

  const toggleVM = (vmid: number) => {
    setSelected((prev) =>
      prev.includes(vmid) ? prev.filter((id) => id !== vmid) : [...prev, vmid],
    );
  };

  const handleAdd = () => {
    if (selected.length === 0) return;
    updatePool.mutate(
      { poolid: poolId, vms: selected.join(",") },
      { onSuccess: () => { setOpen(false); setSelected([]); } },
    );
  };

  return (
    <Dialog open={open} onOpenChange={(v) => { setOpen(v); if (!v) { setSelected([]); setPickerOpen(false); } }}>
      <DialogTrigger asChild>
        <Button size="sm" variant="outline"><Monitor className="mr-2 h-4 w-4" />Add VM</Button>
      </DialogTrigger>
      <DialogContent className="max-w-md">
        <DialogHeader><DialogTitle>Add VMs to {poolId}</DialogTitle></DialogHeader>
        <div className="space-y-4">
          <Popover open={pickerOpen} onOpenChange={setPickerOpen}>
            <PopoverTrigger asChild>
              <Button variant="outline" role="combobox" className="w-full justify-between font-normal">
                {selected.length > 0
                  ? `${String(selected.length)} VM${selected.length > 1 ? "s" : ""} selected`
                  : "Select VMs..."}
                <ChevronsUpDown className="ml-2 h-4 w-4 shrink-0 opacity-50" />
              </Button>
            </PopoverTrigger>
            <PopoverContent className="w-[--radix-popover-trigger-width] p-0" align="start">
              <Command>
                <CommandInput placeholder="Search VMs..." />
                <CommandList>
                  <CommandEmpty>No VMs available</CommandEmpty>
                  <CommandGroup>
                    {availableVMs.map((vm) => (
                      <CommandItem
                        key={vm.vmid}
                        value={`${String(vm.vmid)} ${vm.name}`}
                        onSelect={() => { toggleVM(vm.vmid); }}
                      >
                        <Check className={cn("mr-2 h-4 w-4", selected.includes(vm.vmid) ? "opacity-100" : "opacity-0")} />
                        <span className="mr-2 font-mono text-xs text-muted-foreground">{vm.vmid}</span>
                        <span>{vm.name}</span>
                        <Badge variant="outline" className="ml-auto">{vm.type}</Badge>
                      </CommandItem>
                    ))}
                  </CommandGroup>
                </CommandList>
              </Command>
            </PopoverContent>
          </Popover>

          {selected.length > 0 && (
            <div className="flex flex-wrap gap-1">
              {selected.map((vmid) => {
                const vm = availableVMs.find((v) => v.vmid === vmid);
                return (
                  <Badge key={vmid} variant="secondary" className="gap-1">
                    {vm ? `${vm.name} (${String(vmid)})` : String(vmid)}
                    <button type="button" onClick={() => { toggleVM(vmid); }} className="ml-1 hover:text-destructive">
                      <X className="h-3 w-3" />
                    </button>
                  </Badge>
                );
              })}
            </div>
          )}

          <div className="flex gap-2">
            <Button onClick={handleAdd} disabled={selected.length === 0 || updatePool.isPending}>
              {updatePool.isPending ? "Adding..." : `Add ${selected.length > 0 ? String(selected.length) : ""} VM${selected.length !== 1 ? "s" : ""}`}
            </Button>
            <Button variant="outline" onClick={() => { setOpen(false); }}>Cancel</Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}

// --- Add Storage Dialog ---

function AddStorageDialog({ clusterId, poolId, memberStorageNames }: { clusterId: string; poolId: string; memberStorageNames: Set<string> }) {
  const storageQuery = useClusterStorage(clusterId);
  const updatePool = useUpdatePool(clusterId);
  const [open, setOpen] = useState(false);
  const [selected, setSelected] = useState<string[]>([]);
  const [pickerOpen, setPickerOpen] = useState(false);

  const availableStorage = useMemo(() => {
    const seen = new Set<string>();
    return (storageQuery.data ?? []).filter((s) => {
      if (memberStorageNames.has(s.storage) || seen.has(s.storage)) return false;
      seen.add(s.storage);
      return true;
    });
  }, [storageQuery.data, memberStorageNames]);

  const toggleStorage = (name: string) => {
    setSelected((prev) =>
      prev.includes(name) ? prev.filter((n) => n !== name) : [...prev, name],
    );
  };

  const handleAdd = () => {
    if (selected.length === 0) return;
    updatePool.mutate(
      { poolid: poolId, storage: selected.join(",") },
      { onSuccess: () => { setOpen(false); setSelected([]); } },
    );
  };

  return (
    <Dialog open={open} onOpenChange={(v) => { setOpen(v); if (!v) { setSelected([]); setPickerOpen(false); } }}>
      <DialogTrigger asChild>
        <Button size="sm" variant="outline"><Database className="mr-2 h-4 w-4" />Add Storage</Button>
      </DialogTrigger>
      <DialogContent className="max-w-md">
        <DialogHeader><DialogTitle>Add Storage to {poolId}</DialogTitle></DialogHeader>
        <div className="space-y-4">
          <Popover open={pickerOpen} onOpenChange={setPickerOpen}>
            <PopoverTrigger asChild>
              <Button variant="outline" role="combobox" className="w-full justify-between font-normal">
                {selected.length > 0
                  ? `${String(selected.length)} storage${selected.length > 1 ? "s" : ""} selected`
                  : "Select storage..."}
                <ChevronsUpDown className="ml-2 h-4 w-4 shrink-0 opacity-50" />
              </Button>
            </PopoverTrigger>
            <PopoverContent className="w-[--radix-popover-trigger-width] p-0" align="start">
              <Command>
                <CommandInput placeholder="Search storage..." />
                <CommandList>
                  <CommandEmpty>No storage available</CommandEmpty>
                  <CommandGroup>
                    {availableStorage.map((s) => (
                      <CommandItem
                        key={s.storage}
                        value={s.storage}
                        onSelect={() => { toggleStorage(s.storage); }}
                      >
                        <Check className={cn("mr-2 h-4 w-4", selected.includes(s.storage) ? "opacity-100" : "opacity-0")} />
                        <span>{s.storage}</span>
                        <Badge variant="outline" className="ml-auto">{s.type}</Badge>
                      </CommandItem>
                    ))}
                  </CommandGroup>
                </CommandList>
              </Command>
            </PopoverContent>
          </Popover>

          {selected.length > 0 && (
            <div className="flex flex-wrap gap-1">
              {selected.map((name) => (
                <Badge key={name} variant="secondary" className="gap-1">
                  {name}
                  <button type="button" onClick={() => { toggleStorage(name); }} className="ml-1 hover:text-destructive">
                    <X className="h-3 w-3" />
                  </button>
                </Badge>
              ))}
            </div>
          )}

          <div className="flex gap-2">
            <Button onClick={handleAdd} disabled={selected.length === 0 || updatePool.isPending}>
              {updatePool.isPending ? "Adding..." : `Add ${selected.length > 0 ? String(selected.length) : ""} Storage`}
            </Button>
            <Button variant="outline" onClick={() => { setOpen(false); }}>Cancel</Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}

// --- Pool Members (expandable row with add/remove) ---

function PoolMembers({ clusterId, poolId, canManage }: { clusterId: string; poolId: string; canManage: boolean }) {
  const poolQuery = useResourcePool(clusterId, poolId);
  const updatePool = useUpdatePool(clusterId);

  const memberVMIDs = useMemo(() => {
    const ids = new Set<number>();
    for (const m of poolQuery.data?.members ?? []) {
      if (m.vmid != null) ids.add(m.vmid);
    }
    return ids;
  }, [poolQuery.data?.members]);

  const memberStorageNames = useMemo(() => {
    const names = new Set<string>();
    for (const m of poolQuery.data?.members ?? []) {
      if (m.type === "storage" && m.storage) names.add(m.storage);
    }
    return names;
  }, [poolQuery.data?.members]);

  const handleRemove = (member: PoolMember) => {
    const deleteField = member.type === "storage" ? member.storage ?? member.id : String(member.vmid ?? member.id);
    updatePool.mutate({
      poolid: poolId,
      ...(member.type === "storage"
        ? { storage: deleteField, delete: "1" }
        : { vms: deleteField, delete: "1" }),
    });
  };

  if (poolQuery.isLoading) return <Skeleton className="h-10 w-full" />;

  const members = poolQuery.data?.members ?? [];

  return (
    <div className="space-y-3 py-2">
      {/* Action buttons */}
      {canManage && (
        <div className="flex gap-2">
          <AddVMDialog clusterId={clusterId} poolId={poolId} memberVMIDs={memberVMIDs} />
          <AddStorageDialog clusterId={clusterId} poolId={poolId} memberStorageNames={memberStorageNames} />
        </div>
      )}

      {/* Members table */}
      {members.length > 0 ? (
        <>
          <p className="text-xs font-medium">Members ({members.length})</p>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>ID</TableHead>
                <TableHead>Type</TableHead>
                <TableHead>Name</TableHead>
                <TableHead>Node</TableHead>
                <TableHead>Status</TableHead>
                <TableHead>Memory</TableHead>
                {canManage && <TableHead className="w-10"></TableHead>}
              </TableRow>
            </TableHeader>
            <TableBody>
              {members.map((m) => (
                <TableRow key={m.id}>
                  <TableCell className="font-mono text-xs">{m.id}</TableCell>
                  <TableCell><Badge variant="outline">{m.type}</Badge></TableCell>
                  <TableCell>{m.name ?? m.storage ?? "—"}</TableCell>
                  <TableCell>{m.node}</TableCell>
                  <TableCell><Badge variant={m.status === "running" ? "default" : "secondary"}>{m.status ?? "—"}</Badge></TableCell>
                  <TableCell>{m["maxmem"] != null ? formatBytes(Number(m["maxmem"])) : "—"}</TableCell>
                  {canManage && (
                    <TableCell>
                      <Button variant="ghost" size="sm" onClick={() => { handleRemove(m); }}>
                        <X className="h-3 w-3 text-destructive" />
                      </Button>
                    </TableCell>
                  )}
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </>
      ) : (
        <p className="text-xs text-muted-foreground">No members in this pool.</p>
      )}
    </div>
  );
}
