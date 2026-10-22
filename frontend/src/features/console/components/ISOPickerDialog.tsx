import { useState } from "react";
import { Loader2, Search, Disc, Disc3 } from "lucide-react";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Button } from "@/components/ui/button";
import { formatBytes } from "@/lib/format";
import { useNodeISOs, useMountISO } from "../api/console-queries";

interface ISOPickerDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  clusterId: string;
  node: string;
  vmResourceId: string;
  currentISO: string | null;
}

export function ISOPickerDialog({
  open,
  onOpenChange,
  clusterId,
  node,
  vmResourceId,
  currentISO,
}: ISOPickerDialogProps) {
  const [search, setSearch] = useState("");
  const { data: isos, isLoading } = useNodeISOs(clusterId, node, open);
  const mountISO = useMountISO();

  const filtered = (isos ?? []).filter((iso) =>
    iso.name.toLowerCase().includes(search.toLowerCase()),
  );

  function handleMount(volid: string) {
    mountISO.mutate(
      { clusterId, vmId: vmResourceId, volid },
      {
        onSuccess: () => {
          onOpenChange(false);
        },
      },
    );
  }

  function handleEject() {
    mountISO.mutate(
      { clusterId, vmId: vmResourceId, volid: "none" },
      {
        onSuccess: () => {
          onOpenChange(false);
        },
      },
    );
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>Mount ISO Image</DialogTitle>
        </DialogHeader>

        {currentISO && (
          <div className="flex items-center justify-between rounded-md border border-border bg-muted/50 px-3 py-2 text-sm">
            <div className="flex items-center gap-2 min-w-0">
              <Disc className="h-4 w-4 shrink-0 text-muted-foreground" />
              <span className="truncate font-medium">{currentISO}</span>
            </div>
            <Button
              variant="ghost"
              size="sm"
              className="h-7 shrink-0 gap-1 text-xs"
              onClick={handleEject}
              disabled={mountISO.isPending}
            >
              <Disc3 className="h-3.5 w-3.5" />
              Eject
            </Button>
          </div>
        )}

        <div className="relative">
          <Search className="absolute left-2.5 top-2.5 h-4 w-4 text-muted-foreground" />
          <Input
            placeholder="Search ISOs..."
            value={search}
            onChange={(e) => {
              setSearch(e.target.value);
            }}
            className="pl-9"
          />
        </div>

        <div className="max-h-64 overflow-y-auto rounded-md border">
          {isLoading ? (
            <div className="flex items-center justify-center py-8">
              <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
            </div>
          ) : filtered.length === 0 ? (
            <div className="py-8 text-center text-sm text-muted-foreground">
              {search ? "No ISOs match your search" : "No ISO images found"}
            </div>
          ) : (
            filtered.map((iso) => (
              <button
                key={iso.volid}
                type="button"
                className="flex w-full items-center gap-3 border-b border-border px-3 py-2 text-left text-sm last:border-0 hover:bg-accent"
                onClick={() => {
                  handleMount(iso.volid);
                }}
                disabled={mountISO.isPending}
              >
                <Disc className="h-4 w-4 shrink-0 text-muted-foreground" />
                <div className="min-w-0 flex-1">
                  <div className="truncate font-medium">{iso.name}</div>
                  <div className="text-xs text-muted-foreground">
                    {iso.storage} &middot; {formatBytes(iso.size)}
                  </div>
                </div>
              </button>
            ))
          )}
        </div>

        {mountISO.isPending && (
          <div className="flex items-center gap-2 text-sm text-muted-foreground">
            <Loader2 className="h-4 w-4 animate-spin" />
            {mountISO.variables.volid === "none"
              ? "Ejecting..."
              : "Mounting..."}
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}
