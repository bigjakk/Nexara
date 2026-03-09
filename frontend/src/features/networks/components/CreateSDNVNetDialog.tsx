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
import { Checkbox } from "@/components/ui/checkbox";
import { Plus, Pencil } from "lucide-react";
import {
  useCreateSDNVNet,
  useUpdateSDNVNet,
  useSDNZones,
} from "../api/network-queries";
import type { SDNVNet, CreateSDNVNetRequest } from "../types/network";

interface CreateSDNVNetDialogProps {
  clusterId: string;
  initialData?: SDNVNet;
}

export function CreateSDNVNetDialog({
  clusterId,
  initialData,
}: CreateSDNVNetDialogProps) {
  const isEdit = initialData !== undefined;
  const [open, setOpen] = useState(false);
  const [vnet, setVnet] = useState("");
  const [zone, setZone] = useState("");
  const [tag, setTag] = useState("");
  const [alias, setAlias] = useState("");
  const [vlanaware, setVlanaware] = useState(false);
  const [isolate, setIsolate] = useState(false);

  const { data: zones } = useSDNZones(clusterId);
  const create = useCreateSDNVNet(clusterId);
  const update = useUpdateSDNVNet(clusterId);
  const mutation = isEdit ? update : create;
  const errorMessage =
    mutation.error instanceof Error ? mutation.error.message : "";

  useEffect(() => {
    if (open && initialData) {
      setVnet(initialData.vnet);
      setZone(initialData.zone);
      setTag(initialData.tag ? String(initialData.tag) : "");
      setAlias(initialData.alias ?? "");
      setVlanaware(initialData.vlanaware === 1);
      setIsolate(initialData.isolate === 1);
    }
    if (open && !initialData) {
      setVnet("");
      setZone("");
      setTag("");
      setAlias("");
      setVlanaware(false);
      setIsolate(false);
    }
  }, [open, initialData]);

  const handleSubmit = () => {
    if (!vnet || !zone) return;
    const params: CreateSDNVNetRequest = { vnet, zone };
    if (tag) params.tag = Number(tag);
    if (alias) params.alias = alias;
    if (vlanaware) params.vlanaware = 1;
    if (isolate) params.isolate = 1;

    if (isEdit) {
      const updateParams: Omit<CreateSDNVNetRequest, "vnet"> = Object.fromEntries(
        Object.entries(params).filter(([k]) => k !== "vnet"),
      ) as Omit<CreateSDNVNetRequest, "vnet">;
      update.mutate(
        { vnet: initialData.vnet, params: updateParams },
        { onSuccess: () => { setOpen(false); } },
      );
    } else {
      create.mutate(params, { onSuccess: () => { setOpen(false); } });
    }
  };

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
            Create VNet
          </Button>
        )}
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{isEdit ? "Edit VNet" : "Create SDN VNet"}</DialogTitle>
        </DialogHeader>
        <div className="space-y-4">
          <div className="grid grid-cols-2 gap-4">
            <div className="space-y-2">
              <Label>VNet ID</Label>
              <Input
                placeholder="myvnet"
                value={vnet}
                onChange={(e) => { setVnet(e.target.value); }}
                disabled={isEdit}
              />
            </div>
            <div className="space-y-2">
              <Label>Zone</Label>
              <Select value={zone} onValueChange={setZone}>
                <SelectTrigger>
                  <SelectValue placeholder="Select zone" />
                </SelectTrigger>
                <SelectContent>
                  {zones?.map((z) => (
                    <SelectItem key={z.zone} value={z.zone}>
                      {z.zone} ({z.type})
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          </div>
          <div className="grid grid-cols-2 gap-4">
            <div className="space-y-2">
              <Label>VLAN Tag (optional)</Label>
              <Input
                type="number"
                placeholder="100"
                value={tag}
                onChange={(e) => { setTag(e.target.value); }}
              />
            </div>
            <div className="space-y-2">
              <Label>Alias (optional)</Label>
              <Input
                placeholder="My VNet"
                value={alias}
                onChange={(e) => { setAlias(e.target.value); }}
              />
            </div>
          </div>
          <div className="flex items-center gap-2">
            <Checkbox
              id="vlanaware"
              checked={vlanaware}
              onCheckedChange={(checked) => { setVlanaware(checked === true); }}
            />
            <Label htmlFor="vlanaware">VLAN Aware</Label>
          </div>
          <div className="flex items-center gap-2">
            <Checkbox
              id="isolate"
              checked={isolate}
              onCheckedChange={(checked) => { setIsolate(checked === true); }}
            />
            <Label htmlFor="isolate">Isolate Ports</Label>
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
              disabled={mutation.isPending || !vnet || !zone}
            >
              {isEdit ? "Save" : "Create"}
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}
