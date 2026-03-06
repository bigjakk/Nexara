import { useState, useEffect } from "react";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Plus, Pencil } from "lucide-react";
import { useCreateSDNZone, useUpdateSDNZone } from "../api/network-queries";
import type { SDNZone, CreateSDNZoneRequest } from "../types/network";

interface CreateSDNZoneDialogProps {
  clusterId: string;
  initialData?: SDNZone;
}

const ZONE_TYPES = ["simple", "vlan", "qinq", "vxlan", "evpn"] as const;

export function CreateSDNZoneDialog({
  clusterId,
  initialData,
}: CreateSDNZoneDialogProps) {
  const isEdit = initialData !== undefined;
  const [open, setOpen] = useState(false);
  const [zone, setZone] = useState("");
  const [type, setType] = useState<string>("simple");
  const [bridge, setBridge] = useState("");
  const [tag, setTag] = useState("");
  const [peers, setPeers] = useState("");
  const [mtu, setMtu] = useState("");
  const [nodes, setNodes] = useState("");

  const create = useCreateSDNZone(clusterId);
  const update = useUpdateSDNZone(clusterId);
  const mutation = isEdit ? update : create;
  const errorMessage =
    mutation.error instanceof Error ? mutation.error.message : "";

  useEffect(() => {
    if (open && initialData) {
      setZone(initialData.zone);
      setType(initialData.type);
      setBridge(initialData.bridge ?? "");
      setTag(initialData.tag ? String(initialData.tag) : "");
      setPeers(initialData.peers ?? "");
      setMtu(initialData.mtu ? String(initialData.mtu) : "");
      setNodes(initialData.nodes ?? "");
    }
    if (open && !initialData) {
      setZone("");
      setType("simple");
      setBridge("");
      setTag("");
      setPeers("");
      setMtu("");
      setNodes("");
    }
  }, [open, initialData]);

  const handleSubmit = () => {
    if (!zone || !type) return;
    const params: CreateSDNZoneRequest = { zone, type };
    if (bridge) params.bridge = bridge;
    if (tag) params.tag = Number(tag);
    if (peers) params.peers = peers;
    if (mtu) params.mtu = Number(mtu);
    if (nodes) params.nodes = nodes;

    if (isEdit) {
      const { zone: _z, type: _t, ...updateParams } = params;
      update.mutate(
        { zone: initialData.zone, params: updateParams },
        { onSuccess: () => { setOpen(false); } },
      );
    } else {
      create.mutate(params, { onSuccess: () => { setOpen(false); } });
    }
  };

  const showBridge = type === "vlan" || type === "qinq";
  const showPeers = type === "vxlan" || type === "evpn";
  const showTag = type === "qinq";

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        {isEdit ? (
          <Button variant="ghost" size="icon">
            <Pencil className="h-4 w-4" />
          </Button>
        ) : (
          <Button size="sm">
            <Plus className="mr-1 h-4 w-4" />
            Create Zone
          </Button>
        )}
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{isEdit ? "Edit Zone" : "Create SDN Zone"}</DialogTitle>
        </DialogHeader>
        <div className="space-y-4">
          <div className="grid grid-cols-2 gap-4">
            <div className="space-y-2">
              <Label>Zone ID</Label>
              <Input
                placeholder="myzone"
                value={zone}
                onChange={(e) => { setZone(e.target.value); }}
                disabled={isEdit}
              />
            </div>
            <div className="space-y-2">
              <Label>Type</Label>
              <Select value={type} onValueChange={setType} disabled={isEdit}>
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {ZONE_TYPES.map((t) => (
                    <SelectItem key={t} value={t}>
                      {t}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          </div>
          {showBridge && (
            <div className="space-y-2">
              <Label>Bridge</Label>
              <Input
                placeholder="vmbr0"
                value={bridge}
                onChange={(e) => { setBridge(e.target.value); }}
              />
            </div>
          )}
          {showPeers && (
            <div className="space-y-2">
              <Label>Peers</Label>
              <Input
                placeholder="10.0.0.1,10.0.0.2"
                value={peers}
                onChange={(e) => { setPeers(e.target.value); }}
              />
            </div>
          )}
          {showTag && (
            <div className="space-y-2">
              <Label>Service VLAN Tag</Label>
              <Input
                type="number"
                placeholder="100"
                value={tag}
                onChange={(e) => { setTag(e.target.value); }}
              />
            </div>
          )}
          <div className="grid grid-cols-2 gap-4">
            <div className="space-y-2">
              <Label>MTU (optional)</Label>
              <Input
                type="number"
                placeholder="1500"
                value={mtu}
                onChange={(e) => { setMtu(e.target.value); }}
              />
            </div>
            <div className="space-y-2">
              <Label>Nodes (optional)</Label>
              <Input
                placeholder="node1,node2"
                value={nodes}
                onChange={(e) => { setNodes(e.target.value); }}
              />
            </div>
          </div>
          {errorMessage && (
            <p className="text-sm text-destructive">{errorMessage}</p>
          )}
          <div className="flex justify-end gap-2">
            <Button variant="outline" onClick={() => { setOpen(false); }}>
              Cancel
            </Button>
            <Button
              onClick={handleSubmit}
              disabled={mutation.isPending || !zone || !type}
            >
              {isEdit ? "Save" : "Create"}
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}
