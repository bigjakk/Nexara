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
  useCreateSDNDNS,
  useUpdateSDNDNS,
} from "../api/network-queries";
import type { SDNDNS, CreateSDNDNSRequest } from "../types/network";

interface CreateSDNDNSDialogProps {
  clusterId: string;
  initialData?: SDNDNS;
}

const DNS_TYPES = ["powerdns"] as const;

export function CreateSDNDNSDialog({
  clusterId,
  initialData,
}: CreateSDNDNSDialogProps) {
  const isEdit = initialData !== undefined;
  const [open, setOpen] = useState(false);
  const [dns, setDns] = useState("");
  const [type, setType] = useState<string>("powerdns");
  const [url, setUrl] = useState("");
  const [key, setKey] = useState("");

  const create = useCreateSDNDNS(clusterId);
  const update = useUpdateSDNDNS(clusterId);
  const mutation = isEdit ? update : create;
  const errorMessage =
    mutation.error instanceof Error ? mutation.error.message : "";

  useEffect(() => {
    if (open && initialData) {
      setDns(initialData.dns);
      setType(initialData.type);
      setUrl(initialData.url ?? "");
      setKey(initialData.key ?? "");
    }
    if (open && !initialData) {
      setDns("");
      setType("powerdns");
      setUrl("");
      setKey("");
    }
  }, [open, initialData]);

  const handleSubmit = () => {
    if (!dns || !type) return;
    const params: CreateSDNDNSRequest = { dns, type };
    if (url) params.url = url;
    if (key) params.key = key;

    if (isEdit) {
      const updateParams: Omit<CreateSDNDNSRequest, "dns" | "type"> =
        Object.fromEntries(
          Object.entries(params).filter(
            ([k]) => k !== "dns" && k !== "type",
          ),
        ) as Omit<CreateSDNDNSRequest, "dns" | "type">;
      update.mutate(
        { dns: initialData.dns, params: updateParams },
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
            Create DNS
          </Button>
        )}
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            {isEdit ? "Edit DNS" : "Create SDN DNS Plugin"}
          </DialogTitle>
        </DialogHeader>
        <div className="space-y-4">
          <div className="grid grid-cols-2 gap-4">
            <div className="space-y-2">
              <Label>DNS ID</Label>
              <Input
                placeholder="mydns"
                value={dns}
                onChange={(e) => { setDns(e.target.value); }}
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
                  {DNS_TYPES.map((t) => (
                    <SelectItem key={t} value={t}>
                      {t}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          </div>
          <div className="space-y-2">
            <Label>URL</Label>
            <Input
              placeholder="https://dns.example.com/api"
              value={url}
              onChange={(e) => { setUrl(e.target.value); }}
            />
          </div>
          <div className="space-y-2">
            <Label>API Key</Label>
            <Input
              type="password"
              placeholder="API key"
              value={key}
              onChange={(e) => { setKey(e.target.value); }}
            />
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
              disabled={mutation.isPending || !dns || !type}
            >
              {isEdit ? "Save" : "Create"}
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}
