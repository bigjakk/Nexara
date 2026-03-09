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
  const [ipam, setIpam] = useState("");
  const [dns, setDns] = useState("");
  const [reversedns, setReversedns] = useState("");
  const [dnszone, setDnszone] = useState("");
  const [controller, setController] = useState("");
  const [vrfVxlan, setVrfVxlan] = useState("");
  const [exitnodes, setExitnodes] = useState("");
  const [mac, setMac] = useState("");
  const [advertiseSubnets, setAdvertiseSubnets] = useState(false);
  const [disableArpNdSuppression, setDisableArpNdSuppression] = useState(false);

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
      setIpam(initialData.ipam ?? "");
      setDns(initialData.dns ?? "");
      setReversedns(initialData.reversedns ?? "");
      setDnszone(initialData.dnszone ?? "");
      setController(initialData.controller ?? "");
      setVrfVxlan(initialData["vrf-vxlan"] ? String(initialData["vrf-vxlan"]) : "");
      setExitnodes(initialData.exitnodes ?? "");
      setMac(initialData.mac ?? "");
      setAdvertiseSubnets(initialData["advertise-subnets"] === 1);
      setDisableArpNdSuppression(initialData["disable-arp-nd-suppression"] === 1);
    }
    if (open && !initialData) {
      setZone("");
      setType("simple");
      setBridge("");
      setTag("");
      setPeers("");
      setMtu("");
      setNodes("");
      setIpam("");
      setDns("");
      setReversedns("");
      setDnszone("");
      setController("");
      setVrfVxlan("");
      setExitnodes("");
      setMac("");
      setAdvertiseSubnets(false);
      setDisableArpNdSuppression(false);
    }
  }, [open, initialData]);

  const handleSubmit = () => {
    if (!zone || !type) return;
    const params: CreateSDNZoneRequest = {
      zone,
      type,
      ...(bridge ? { bridge } : {}),
      ...(tag ? { tag: Number(tag) } : {}),
      ...(peers ? { peers } : {}),
      ...(mtu ? { mtu: Number(mtu) } : {}),
      ...(nodes ? { nodes } : {}),
      ...(ipam ? { ipam } : {}),
      ...(dns ? { dns } : {}),
      ...(reversedns ? { reversedns } : {}),
      ...(dnszone ? { dnszone } : {}),
      ...(controller ? { controller } : {}),
      ...(vrfVxlan ? { "vrf-vxlan": Number(vrfVxlan) } : {}),
      ...(exitnodes ? { exitnodes } : {}),
      ...(mac ? { mac } : {}),
      ...(advertiseSubnets ? { "advertise-subnets": 1 } : {}),
      ...(disableArpNdSuppression ? { "disable-arp-nd-suppression": 1 } : {}),
    };

    if (isEdit) {
      const updateParams: Omit<CreateSDNZoneRequest, "zone" | "type"> = Object.fromEntries(
        Object.entries(params).filter(([k]) => k !== "zone" && k !== "type"),
      ) as Omit<CreateSDNZoneRequest, "zone" | "type">;
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
  const showEvpn = type === "evpn";

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
      <DialogContent className="max-h-[85vh] overflow-y-auto sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{isEdit ? "Edit Zone" : "Create SDN Zone"}</DialogTitle>
        </DialogHeader>
        <div className="space-y-4">
          {/* Zone ID & Type */}
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

          {/* Bridge — vlan / qinq */}
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

          {/* Service VLAN Tag — qinq */}
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

          {/* Peers — vxlan / evpn */}
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

          {/* EVPN-only fields */}
          {showEvpn && (
            <>
              <div className="grid grid-cols-2 gap-4">
                <div className="space-y-2">
                  <Label>Controller</Label>
                  <Input
                    placeholder="evpnctl"
                    value={controller}
                    onChange={(e) => { setController(e.target.value); }}
                  />
                </div>
                <div className="space-y-2">
                  <Label>VRF VXLAN Tag</Label>
                  <Input
                    type="number"
                    placeholder="10000"
                    value={vrfVxlan}
                    onChange={(e) => { setVrfVxlan(e.target.value); }}
                  />
                </div>
              </div>
              <div className="grid grid-cols-2 gap-4">
                <div className="space-y-2">
                  <Label>Exit Nodes</Label>
                  <Input
                    placeholder="node1,node2"
                    value={exitnodes}
                    onChange={(e) => { setExitnodes(e.target.value); }}
                  />
                </div>
                <div className="space-y-2">
                  <Label>MAC Address</Label>
                  <Input
                    placeholder="auto"
                    value={mac}
                    onChange={(e) => { setMac(e.target.value); }}
                  />
                </div>
              </div>
              <div className="flex items-center gap-6">
                <div className="flex items-center gap-2">
                  <Checkbox
                    id="advertise-subnets"
                    checked={advertiseSubnets}
                    onCheckedChange={(checked) => { setAdvertiseSubnets(checked === true); }}
                  />
                  <Label htmlFor="advertise-subnets" className="cursor-pointer">
                    Advertise Subnets
                  </Label>
                </div>
                <div className="flex items-center gap-2">
                  <Checkbox
                    id="disable-arp-nd-suppression"
                    checked={disableArpNdSuppression}
                    onCheckedChange={(checked) => { setDisableArpNdSuppression(checked === true); }}
                  />
                  <Label htmlFor="disable-arp-nd-suppression" className="cursor-pointer">
                    Disable ARP ND Suppression
                  </Label>
                </div>
              </div>
            </>
          )}

          {/* MTU & Nodes — all types */}
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

          {/* IPAM & DNS — all types */}
          <div className="grid grid-cols-2 gap-4">
            <div className="space-y-2">
              <Label>IPAM (optional)</Label>
              <Input
                placeholder="pve-ipam"
                value={ipam}
                onChange={(e) => { setIpam(e.target.value); }}
              />
            </div>
            <div className="space-y-2">
              <Label>DNS (optional)</Label>
              <Input
                placeholder="powerdns"
                value={dns}
                onChange={(e) => { setDns(e.target.value); }}
              />
            </div>
          </div>
          <div className="grid grid-cols-2 gap-4">
            <div className="space-y-2">
              <Label>Reverse DNS (optional)</Label>
              <Input
                placeholder="168.192.in-addr.arpa"
                value={reversedns}
                onChange={(e) => { setReversedns(e.target.value); }}
              />
            </div>
            <div className="space-y-2">
              <Label>DNS Zone (optional)</Label>
              <Input
                placeholder="myzone.example.com"
                value={dnszone}
                onChange={(e) => { setDnszone(e.target.value); }}
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
