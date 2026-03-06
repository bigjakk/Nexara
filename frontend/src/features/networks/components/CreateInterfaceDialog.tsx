import { useState } from "react";
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
import { Plus } from "lucide-react";
import { useCreateNetworkInterface } from "../api/network-queries";
import type { CreateNetworkInterfaceRequest } from "../types/network";

interface CreateInterfaceDialogProps {
  clusterId: string;
  nodeName: string;
}

export function CreateInterfaceDialog({
  clusterId,
  nodeName,
}: CreateInterfaceDialogProps) {
  const [open, setOpen] = useState(false);
  const [iface, setIface] = useState("");
  const [type, setType] = useState("bridge");
  const [cidr, setCidr] = useState("");
  const [gateway, setGateway] = useState("");
  const [bridgePorts, setBridgePorts] = useState("");
  const [autostart, setAutostart] = useState(true);

  const create = useCreateNetworkInterface(clusterId, nodeName);

  const handleSubmit = () => {
    const req: CreateNetworkInterfaceRequest = {
      iface,
      type,
      autostart: autostart ? 1 : 0,
    };
    if (cidr) req.cidr = cidr;
    if (gateway) req.gateway = gateway;
    if (bridgePorts) req.bridge_ports = bridgePorts;
    create.mutate(
      req,
      {
        onSuccess: () => {
          setOpen(false);
          setIface("");
          setCidr("");
          setGateway("");
          setBridgePorts("");
        },
      },
    );
  };

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button size="sm">
          <Plus className="mr-1 h-4 w-4" />
          Create Interface
        </Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            Create Network Interface on {nodeName}
          </DialogTitle>
        </DialogHeader>
        <div className="space-y-4">
          <div className="space-y-2">
            <Label>Interface Name</Label>
            <Input
              placeholder="e.g. vmbr1"
              value={iface}
              onChange={(e) => { setIface(e.target.value); }}
            />
          </div>
          <div className="space-y-2">
            <Label>Type</Label>
            <Select value={type} onValueChange={setType}>
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="bridge">Bridge</SelectItem>
                <SelectItem value="bond">Bond</SelectItem>
                <SelectItem value="eth">Ethernet</SelectItem>
                <SelectItem value="vlan">VLAN</SelectItem>
                <SelectItem value="OVSBridge">OVS Bridge</SelectItem>
                <SelectItem value="OVSPort">OVS Port</SelectItem>
              </SelectContent>
            </Select>
          </div>
          <div className="space-y-2">
            <Label>CIDR (optional)</Label>
            <Input
              placeholder="e.g. 10.0.0.1/24"
              value={cidr}
              onChange={(e) => { setCidr(e.target.value); }}
            />
          </div>
          <div className="space-y-2">
            <Label>Gateway (optional)</Label>
            <Input
              placeholder="e.g. 10.0.0.1"
              value={gateway}
              onChange={(e) => { setGateway(e.target.value); }}
            />
          </div>
          {(type === "bridge" || type === "OVSBridge") && (
            <div className="space-y-2">
              <Label>Bridge Ports (optional)</Label>
              <Input
                placeholder="e.g. eno1"
                value={bridgePorts}
                onChange={(e) => { setBridgePorts(e.target.value); }}
              />
            </div>
          )}
          <div className="flex items-center gap-2">
            <input
              type="checkbox"
              id="autostart"
              checked={autostart}
              onChange={(e) => { setAutostart(e.target.checked); }}
              className="rounded border"
            />
            <Label htmlFor="autostart">Autostart</Label>
          </div>
          <div className="flex justify-end gap-2">
            <Button variant="outline" onClick={() => { setOpen(false); }}>
              Cancel
            </Button>
            <Button
              onClick={handleSubmit}
              disabled={!iface || !type || create.isPending}
            >
              Create
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}
