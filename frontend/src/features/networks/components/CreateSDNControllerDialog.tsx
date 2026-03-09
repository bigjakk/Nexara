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
import {
  useCreateSDNController,
  useUpdateSDNController,
} from "../api/network-queries";
import type {
  SDNController,
  CreateSDNControllerRequest,
} from "../types/network";

interface CreateSDNControllerDialogProps {
  clusterId: string;
  initialData?: SDNController;
}

const CONTROLLER_TYPES = ["evpn", "bgp", "isis"] as const;

export function CreateSDNControllerDialog({
  clusterId,
  initialData,
}: CreateSDNControllerDialogProps) {
  const isEdit = initialData !== undefined;
  const [open, setOpen] = useState(false);
  const [controller, setController] = useState("");
  const [type, setType] = useState<string>("evpn");
  const [asn, setAsn] = useState("");
  const [peers, setPeers] = useState("");
  const [nodes, setNodes] = useState("");
  const [loopback, setLoopback] = useState("");
  const [node, setNode] = useState("");
  const [isisDomain, setIsisDomain] = useState("");
  const [isisIfaces, setIsisIfaces] = useState("");
  const [isisNet, setIsisNet] = useState("");
  const [ebgpMultihop, setEbgpMultihop] = useState("");

  const create = useCreateSDNController(clusterId);
  const update = useUpdateSDNController(clusterId);
  const mutation = isEdit ? update : create;
  const errorMessage =
    mutation.error instanceof Error ? mutation.error.message : "";

  useEffect(() => {
    if (open && initialData) {
      setController(initialData.controller);
      setType(initialData.type);
      setAsn(initialData.asn ? String(initialData.asn) : "");
      setPeers(initialData.peers ?? "");
      setNodes(initialData.nodes ?? "");
      setLoopback(initialData.loopback ?? "");
      setNode(initialData.node ?? "");
      setIsisDomain(initialData["isis-domain"] ?? "");
      setIsisIfaces(initialData["isis-ifaces"] ?? "");
      setIsisNet(initialData["isis-net"] ?? "");
      setEbgpMultihop(
        initialData["ebgp-multihop"] !== undefined
          ? String(initialData["ebgp-multihop"])
          : "",
      );
    }
    if (open && !initialData) {
      setController("");
      setType("evpn");
      setAsn("");
      setPeers("");
      setNodes("");
      setLoopback("");
      setNode("");
      setIsisDomain("");
      setIsisIfaces("");
      setIsisNet("");
      setEbgpMultihop("");
    }
  }, [open, initialData]);

  const handleSubmit = () => {
    if (!controller || !type) return;
    const params: CreateSDNControllerRequest = { controller, type };
    if (asn) params.asn = Number(asn);
    if (peers) params.peers = peers;
    if (nodes) params.nodes = nodes;
    if (loopback) params.loopback = loopback;
    if (node) params.node = node;
    if (isisDomain) params["isis-domain"] = isisDomain;
    if (isisIfaces) params["isis-ifaces"] = isisIfaces;
    if (isisNet) params["isis-net"] = isisNet;
    if (ebgpMultihop) params["ebgp-multihop"] = Number(ebgpMultihop);

    if (isEdit) {
      const updateParams: Omit<CreateSDNControllerRequest, "controller" | "type"> =
        Object.fromEntries(
          Object.entries(params).filter(
            ([k]) => k !== "controller" && k !== "type",
          ),
        ) as Omit<CreateSDNControllerRequest, "controller" | "type">;
      update.mutate(
        { controller: initialData.controller, params: updateParams },
        { onSuccess: () => { setOpen(false); } },
      );
    } else {
      create.mutate(params, { onSuccess: () => { setOpen(false); } });
    }
  };

  const showBgpFields = type === "evpn" || type === "bgp";
  const showIsisFields = type === "isis";

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
            Create Controller
          </Button>
        )}
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            {isEdit ? "Edit Controller" : "Create SDN Controller"}
          </DialogTitle>
        </DialogHeader>
        <div className="space-y-4">
          <div className="grid grid-cols-2 gap-4">
            <div className="space-y-2">
              <Label>Controller ID</Label>
              <Input
                placeholder="myctrl"
                value={controller}
                onChange={(e) => { setController(e.target.value); }}
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
                  {CONTROLLER_TYPES.map((t) => (
                    <SelectItem key={t} value={t}>
                      {t}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          </div>
          {showBgpFields && (
            <>
              <div className="grid grid-cols-2 gap-4">
                <div className="space-y-2">
                  <Label>ASN</Label>
                  <Input
                    type="number"
                    placeholder="65000"
                    value={asn}
                    onChange={(e) => { setAsn(e.target.value); }}
                  />
                </div>
                <div className="space-y-2">
                  <Label>Peers (optional)</Label>
                  <Input
                    placeholder="10.0.0.1,10.0.0.2"
                    value={peers}
                    onChange={(e) => { setPeers(e.target.value); }}
                  />
                </div>
              </div>
              <div className="grid grid-cols-2 gap-4">
                <div className="space-y-2">
                  <Label>eBGP Multihop (optional)</Label>
                  <Input
                    type="number"
                    placeholder="10"
                    value={ebgpMultihop}
                    onChange={(e) => { setEbgpMultihop(e.target.value); }}
                  />
                </div>
                <div className="space-y-2">
                  <Label>Loopback (optional)</Label>
                  <Input
                    placeholder="lo"
                    value={loopback}
                    onChange={(e) => { setLoopback(e.target.value); }}
                  />
                </div>
              </div>
            </>
          )}
          {showIsisFields && (
            <>
              <div className="grid grid-cols-2 gap-4">
                <div className="space-y-2">
                  <Label>ISIS Domain</Label>
                  <Input
                    placeholder="example.com"
                    value={isisDomain}
                    onChange={(e) => { setIsisDomain(e.target.value); }}
                  />
                </div>
                <div className="space-y-2">
                  <Label>ISIS Interfaces</Label>
                  <Input
                    placeholder="eth0,eth1"
                    value={isisIfaces}
                    onChange={(e) => { setIsisIfaces(e.target.value); }}
                  />
                </div>
              </div>
              <div className="space-y-2">
                <Label>ISIS NET</Label>
                <Input
                  placeholder="49.0001.0000.0000.0001.00"
                  value={isisNet}
                  onChange={(e) => { setIsisNet(e.target.value); }}
                />
              </div>
            </>
          )}
          <div className="grid grid-cols-2 gap-4">
            <div className="space-y-2">
              <Label>Nodes (optional)</Label>
              <Input
                placeholder="node1,node2"
                value={nodes}
                onChange={(e) => { setNodes(e.target.value); }}
              />
            </div>
            <div className="space-y-2">
              <Label>Node (optional)</Label>
              <Input
                placeholder="node1"
                value={node}
                onChange={(e) => { setNode(e.target.value); }}
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
              disabled={mutation.isPending || !controller || !type}
            >
              {isEdit ? "Save" : "Create"}
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}
