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
import { useCreateSDNIPAM, useUpdateSDNIPAM } from "../api/network-queries";
import { useSaveOutcome } from "@/hooks/useSaveOutcome";
import type { SDNIPAM, CreateSDNIPAMRequest } from "../types/network";
import { useTranslation } from "react-i18next";

interface CreateSDNIPAMDialogProps {
  clusterId: string;
  initialData?: SDNIPAM;
  /** With initialData: show the IPAM without letting it change, for a role
   *  without manage:network. */
  readOnly?: boolean;
}

const IPAM_TYPES = ["pve", "netbox", "phpipam"] as const;

export function CreateSDNIPAMDialog({
  clusterId,
  initialData,
  readOnly = false,
}: CreateSDNIPAMDialogProps) {
  const { t } = useTranslation("networks");
  const isEdit = initialData !== undefined;
  const [open, setOpen] = useState(false);
  const [ipam, setIpam] = useState("");
  const [type, setType] = useState<string>("pve");
  const [url, setUrl] = useState("");
  const [token, setToken] = useState("");
  const [section, setSection] = useState("");

  const create = useCreateSDNIPAM(clusterId);
  const settle = useSaveOutcome(open);
  const update = useUpdateSDNIPAM(clusterId);
  const mutation = isEdit ? update : create;
  const errorMessage =
    mutation.error instanceof Error ? mutation.error.message : "";

  useEffect(() => {
    if (open && initialData) {
      setIpam(initialData.ipam);
      setType(initialData.type);
      setUrl(initialData.url ?? "");
      setToken(initialData.token ?? "");
      setSection(
        initialData.section !== undefined ? String(initialData.section) : "",
      );
    }
    if (open && !initialData) {
      setIpam("");
      setType("pve");
      setUrl("");
      setToken("");
      setSection("");
    }
  }, [open, initialData]);

  const handleSubmit = () => {
    if (!ipam || !type) return;
    const params: CreateSDNIPAMRequest = { ipam, type };
    if (url) params.url = url;
    if (token) params.token = token;
    if (section) params.section = Number(section);

    if (isEdit) {
      const updateParams: Omit<CreateSDNIPAMRequest, "ipam" | "type"> =
        Object.fromEntries(
          Object.entries(params).filter(([k]) => k !== "ipam" && k !== "type"),
        );
      settle(
        update.mutateAsync({ ipam: initialData.ipam, params: updateParams }),
        {
          action: t("sdnIPAMActions.saving", { ipam: initialData.ipam }),
          onSuccess: () => {
            setOpen(false);
          },
          onError: () => undefined, // shown below from the mutation's state
        },
      );
    } else {
      settle(create.mutateAsync(params), {
        action: t("sdnIPAMActions.creating", { ipam }),
        onSuccess: () => {
          setOpen(false);
        },
        onError: () => undefined, // shown below from the mutation's state
      });
    }
  };

  const showExternalFields = type !== "pve";

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        {isEdit ? (
          <Button
            aria-label={`${readOnly ? "View" : "Edit"} IPAM ${initialData.ipam}`}
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
            Create IPAM
          </Button>
        )}
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            {readOnly ? "View IPAM" : isEdit ? "Edit IPAM" : "Create SDN IPAM"}
          </DialogTitle>
        </DialogHeader>
        <fieldset disabled={readOnly} className="min-w-0 space-y-4">
          <div className="grid grid-cols-2 gap-4">
            <div className="space-y-2">
              <Label>IPAM ID</Label>
              <Input
                placeholder="myipam"
                value={ipam}
                onChange={(e) => {
                  setIpam(e.target.value);
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
                  {IPAM_TYPES.map((t) => (
                    <SelectItem key={t} value={t}>
                      {t}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          </div>
          {showExternalFields && (
            <>
              <div className="space-y-2">
                <Label>URL</Label>
                <Input
                  placeholder="https://ipam.example.com/api"
                  value={url}
                  onChange={(e) => {
                    setUrl(e.target.value);
                  }}
                />
              </div>
              <div className="grid grid-cols-2 gap-4">
                <div className="space-y-2">
                  <Label>Token</Label>
                  <Input
                    type="password"
                    placeholder="API token"
                    value={token}
                    onChange={(e) => {
                      setToken(e.target.value);
                    }}
                  />
                </div>
                <div className="space-y-2">
                  <Label>Section (optional)</Label>
                  <Input
                    type="number"
                    placeholder="1"
                    value={section}
                    onChange={(e) => {
                      setSection(e.target.value);
                    }}
                  />
                </div>
              </div>
            </>
          )}
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
              disabled={mutation.isPending || !ipam || !type}
            >
              {isEdit ? "Save" : "Create"}
            </Button>
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}
