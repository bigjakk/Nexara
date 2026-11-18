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
import { Eye, Plus, Pencil } from "lucide-react";
import { useCreateSDNSubnet, useUpdateSDNSubnet } from "../api/network-queries";
import { useSaveOutcome } from "@/hooks/useSaveOutcome";
import type { SDNSubnet, CreateSDNSubnetRequest } from "../types/network";
import { useTranslation } from "react-i18next";

interface CreateSDNSubnetDialogProps {
  clusterId: string;
  vnet: string;
  initialData?: SDNSubnet;
  /** With initialData: show the subnet without letting it change, for a role
   *  without manage:network. */
  readOnly?: boolean;
}

export function CreateSDNSubnetDialog({
  clusterId,
  vnet,
  initialData,
  readOnly = false,
}: CreateSDNSubnetDialogProps) {
  const { t } = useTranslation("networks");
  const isEdit = initialData !== undefined;
  const [open, setOpen] = useState(false);
  const [subnet, setSubnet] = useState("");
  const [gateway, setGateway] = useState("");
  const [snat, setSnat] = useState(false);
  const [dhcpRange, setDhcpRange] = useState("");
  const [dhcpDnsServer, setDhcpDnsServer] = useState("");

  const create = useCreateSDNSubnet(clusterId, vnet);
  const settle = useSaveOutcome(open);
  const update = useUpdateSDNSubnet(clusterId, vnet);
  const mutation = isEdit ? update : create;
  const errorMessage =
    mutation.error instanceof Error ? mutation.error.message : "";

  useEffect(() => {
    if (open && initialData) {
      setSubnet(initialData.subnet);
      setGateway(initialData.gateway ?? "");
      setSnat(initialData.snat === 1);
      setDhcpRange(initialData["dhcp-range"] ?? "");
      setDhcpDnsServer(initialData["dhcp-dns-server"] ?? "");
    }
    if (open && !initialData) {
      setSubnet("");
      setGateway("");
      setSnat(false);
      setDhcpRange("");
      setDhcpDnsServer("");
    }
  }, [open, initialData]);

  const handleSubmit = () => {
    if (!subnet) return;
    const params: CreateSDNSubnetRequest = { subnet, type: "subnet" };
    if (gateway) params.gateway = gateway;
    if (snat) params.snat = 1;
    if (dhcpRange) params["dhcp-range"] = dhcpRange;
    if (dhcpDnsServer) params["dhcp-dns-server"] = dhcpDnsServer;

    if (isEdit) {
      const updateParams: {
        gateway?: string;
        snat?: number;
        "dhcp-range"?: string;
        "dhcp-dns-server"?: string;
      } = {
        snat: snat ? 1 : 0,
      };
      if (gateway) updateParams.gateway = gateway;
      if (dhcpRange) updateParams["dhcp-range"] = dhcpRange;
      if (dhcpDnsServer) updateParams["dhcp-dns-server"] = dhcpDnsServer;
      settle(
        update.mutateAsync({
          subnet: initialData.subnet,
          params: updateParams,
        }),
        {
          action: t("sdnSubnetActions.saving", { subnet: initialData.subnet }),
          onSuccess: () => {
            setOpen(false);
          },
          onError: () => undefined, // shown below from the mutation's state
        },
      );
    } else {
      settle(create.mutateAsync(params), {
        action: t("sdnSubnetActions.creating", { subnet }),
        onSuccess: () => {
          setOpen(false);
        },
        onError: () => undefined, // shown below from the mutation's state
      });
    }
  };

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        {isEdit ? (
          <Button
            aria-label={`${readOnly ? "View" : "Edit"} Subnet ${initialData.subnet}`}
            variant="ghost"
            size="icon"
            className="h-8 w-8 text-muted-foreground"
          >
            {readOnly ? (
              <Eye className="h-4 w-4" />
            ) : (
              <Pencil className="h-4 w-4" />
            )}
          </Button>
        ) : (
          <Button variant="outline" size="sm">
            <Plus className="h-4 w-4" />
            Add subnet
          </Button>
        )}
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            {readOnly
              ? "View subnet"
              : isEdit
                ? "Edit subnet"
                : `Add subnet to ${vnet}`}
          </DialogTitle>
        </DialogHeader>
        <fieldset disabled={readOnly} className="min-w-0 space-y-4">
          <div className="space-y-2">
            <Label>Subnet CIDR</Label>
            <Input
              placeholder="10.0.0.0/24"
              value={subnet}
              onChange={(e) => {
                setSubnet(e.target.value);
              }}
              disabled={isEdit}
            />
          </div>
          <div className="space-y-2">
            <Label>Gateway (optional)</Label>
            <Input
              placeholder="10.0.0.1"
              value={gateway}
              onChange={(e) => {
                setGateway(e.target.value);
              }}
            />
          </div>
          <div className="flex items-center gap-2">
            <Checkbox
              id="snat"
              checked={snat}
              onCheckedChange={(checked) => {
                setSnat(checked === true);
              }}
            />
            <Label htmlFor="snat">Enable SNAT</Label>
          </div>
          <div className="space-y-2">
            <Label>DHCP range (optional)</Label>
            <Input
              placeholder="start-address=10.0.0.50,end-address=10.0.0.200"
              value={dhcpRange}
              onChange={(e) => {
                setDhcpRange(e.target.value);
              }}
            />
          </div>
          <div className="space-y-2">
            <Label>DHCP DNS server (optional)</Label>
            <Input
              placeholder="8.8.8.8"
              value={dhcpDnsServer}
              onChange={(e) => {
                setDhcpDnsServer(e.target.value);
              }}
            />
          </div>
        </fieldset>
        {errorMessage && (
          <p className="text-sm text-destructive">{errorMessage}</p>
        )}
        <div className="flex justify-end gap-2">
          <Button
            variant="outline"
            onClick={() => {
              setOpen(false);
            }}
          >
            {readOnly ? "Close" : "Cancel"}
          </Button>
          {!readOnly && (
            <Button
              onClick={handleSubmit}
              disabled={mutation.isPending || !subnet}
            >
              {isEdit ? "Save" : "Create"}
            </Button>
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}
