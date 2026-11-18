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
import { Eye, Plus, Pencil } from "lucide-react";
import { useCreateSDNDNS, useUpdateSDNDNS } from "../api/network-queries";
import { useSaveOutcome } from "@/hooks/useSaveOutcome";
import type { SDNDNS, CreateSDNDNSRequest } from "../types/network";
import { useTranslation } from "react-i18next";

interface CreateSDNDNSDialogProps {
  clusterId: string;
  initialData?: SDNDNS;
  /** With initialData: show the DNS plugin without letting it change, for a
   *  role without manage:network. */
  readOnly?: boolean;
}

const DNS_TYPES = ["powerdns"] as const;

export function CreateSDNDNSDialog({
  clusterId,
  initialData,
  readOnly = false,
}: CreateSDNDNSDialogProps) {
  const { t } = useTranslation("networks");
  const isEdit = initialData !== undefined;
  const [open, setOpen] = useState(false);
  const [dns, setDns] = useState("");
  const [type, setType] = useState<string>("powerdns");
  const [url, setUrl] = useState("");
  const [key, setKey] = useState("");

  const create = useCreateSDNDNS(clusterId);
  const settle = useSaveOutcome(open);
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
          Object.entries(params).filter(([k]) => k !== "dns" && k !== "type"),
        );
      settle(
        update.mutateAsync({ dns: initialData.dns, params: updateParams }),
        {
          action: t("sdnDNSActions.saving", { dns: initialData.dns }),
          onSuccess: () => {
            setOpen(false);
          },
          onError: () => undefined, // shown below from the mutation's state
        },
      );
    } else {
      settle(create.mutateAsync(params), {
        action: t("sdnDNSActions.creating", { dns }),
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
            aria-label={`${readOnly ? "View" : "Edit"} DNS ${initialData.dns}`}
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
          <Button size="sm">
            <Plus className="h-4 w-4" />
            Create DNS
          </Button>
        )}
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            {readOnly
              ? "View DNS"
              : isEdit
                ? "Edit DNS"
                : "Create SDN DNS plugin"}
          </DialogTitle>
        </DialogHeader>
        <fieldset disabled={readOnly} className="min-w-0 space-y-4">
          <div className="grid grid-cols-2 gap-4">
            <div className="space-y-2">
              <Label>DNS ID</Label>
              <Input
                placeholder="mydns"
                value={dns}
                onChange={(e) => {
                  setDns(e.target.value);
                }}
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
              onChange={(e) => {
                setUrl(e.target.value);
              }}
            />
          </div>
          <div className="space-y-2">
            <Label>API key</Label>
            <Input
              type="password"
              placeholder="API key"
              value={key}
              onChange={(e) => {
                setKey(e.target.value);
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
              disabled={mutation.isPending || !dns || !type}
            >
              {isEdit ? "Save" : "Create"}
            </Button>
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}
