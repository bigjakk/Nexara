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
import { Checkbox } from "@/components/ui/checkbox";
import { Plus, Pencil } from "lucide-react";
import { useCreateSDNSubnet, useUpdateSDNSubnet } from "../api/network-queries";
import type { SDNSubnet, CreateSDNSubnetRequest } from "../types/network";

interface CreateSDNSubnetDialogProps {
  clusterId: string;
  vnet: string;
  initialData?: SDNSubnet;
}

export function CreateSDNSubnetDialog({
  clusterId,
  vnet,
  initialData,
}: CreateSDNSubnetDialogProps) {
  const isEdit = initialData !== undefined;
  const [open, setOpen] = useState(false);
  const [subnet, setSubnet] = useState("");
  const [gateway, setGateway] = useState("");
  const [snat, setSnat] = useState(false);

  const create = useCreateSDNSubnet(clusterId, vnet);
  const update = useUpdateSDNSubnet(clusterId, vnet);
  const mutation = isEdit ? update : create;
  const errorMessage =
    mutation.error instanceof Error ? mutation.error.message : "";

  useEffect(() => {
    if (open && initialData) {
      setSubnet(initialData.subnet);
      setGateway(initialData.gateway ?? "");
      setSnat(initialData.snat === 1);
    }
    if (open && !initialData) {
      setSubnet("");
      setGateway("");
      setSnat(false);
    }
  }, [open, initialData]);

  const handleSubmit = () => {
    if (!subnet) return;
    const params: CreateSDNSubnetRequest = { subnet };
    if (gateway) params.gateway = gateway;
    if (snat) params.snat = 1;

    if (isEdit) {
      const updateParams: { gateway?: string; snat?: number } = {
        snat: snat ? 1 : 0,
      };
      if (gateway) updateParams.gateway = gateway;
      update.mutate(
        { subnet: initialData.subnet, params: updateParams },
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
          <Button variant="outline" size="sm">
            <Plus className="mr-1 h-4 w-4" />
            Add Subnet
          </Button>
        )}
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            {isEdit ? "Edit Subnet" : `Add Subnet to ${vnet}`}
          </DialogTitle>
        </DialogHeader>
        <div className="space-y-4">
          <div className="space-y-2">
            <Label>Subnet CIDR</Label>
            <Input
              placeholder="10.0.0.0/24"
              value={subnet}
              onChange={(e) => { setSubnet(e.target.value); }}
              disabled={isEdit}
            />
          </div>
          <div className="space-y-2">
            <Label>Gateway (optional)</Label>
            <Input
              placeholder="10.0.0.1"
              value={gateway}
              onChange={(e) => { setGateway(e.target.value); }}
            />
          </div>
          <div className="flex items-center gap-2">
            <Checkbox
              id="snat"
              checked={snat}
              onCheckedChange={(checked) => { setSnat(checked === true); }}
            />
            <Label htmlFor="snat">Enable SNAT</Label>
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
              disabled={mutation.isPending || !subnet}
            >
              {isEdit ? "Save" : "Create"}
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}
