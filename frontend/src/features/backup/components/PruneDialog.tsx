import { useState } from "react";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { Scissors } from "lucide-react";
import { usePruneDatastore } from "../api/backup-queries";
import type { PBSPruneResult } from "../types/backup";

interface PruneDialogProps {
  pbsId: string;
  store: string;
}

function formatUnixTime(ts: number): string {
  return new Date(ts * 1000).toLocaleString();
}

export function PruneDialog({ pbsId, store }: PruneDialogProps) {
  const [open, setOpen] = useState(false);
  const [keepLast, setKeepLast] = useState(3);
  const [keepDaily, setKeepDaily] = useState(7);
  const [keepWeekly, setKeepWeekly] = useState(4);
  const [keepMonthly, setKeepMonthly] = useState(6);
  const [keepYearly, setKeepYearly] = useState(1);
  const [preview, setPreview] = useState<PBSPruneResult[] | null>(null);

  const pruneMutation = usePruneDatastore();

  const body = {
    dry_run: true,
    keep_last: keepLast,
    keep_daily: keepDaily,
    keep_weekly: keepWeekly,
    keep_monthly: keepMonthly,
    keep_yearly: keepYearly,
  };

  const handlePreview = () => {
    pruneMutation.mutate(
      { pbsId, store, body: { ...body, dry_run: true } },
      {
        onSuccess: (results) => {
          setPreview(results);
        },
      },
    );
  };

  const handleExecute = () => {
    pruneMutation.mutate(
      { pbsId, store, body: { ...body, dry_run: false } },
      {
        onSuccess: () => {
          setOpen(false);
          setPreview(null);
        },
      },
    );
  };

  const toRemove = preview?.filter((r) => !r.keep && !r.protected) ?? [];
  const toKeep = preview?.filter((r) => r.keep || r.protected) ?? [];

  return (
    <Dialog
      open={open}
      onOpenChange={(v) => {
        setOpen(v);
        if (!v) setPreview(null);
      }}
    >
      <DialogTrigger asChild>
        <Button variant="outline" size="sm">
          <Scissors className="mr-2 h-4 w-4" />
          Prune
        </Button>
      </DialogTrigger>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>Prune Datastore: {store}</DialogTitle>
          <DialogDescription>
            Configure retention policy and preview which snapshots will be
            removed.
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-4 py-4">
          <div className="grid grid-cols-5 gap-3">
            <div className="space-y-1">
              <Label className="text-xs">Last</Label>
              <Input
                type="number"
                min={0}
                value={keepLast}
                onChange={(e) => {
                  setKeepLast(Number(e.target.value));
                }}
                className="h-8 text-sm"
              />
            </div>
            <div className="space-y-1">
              <Label className="text-xs">Daily</Label>
              <Input
                type="number"
                min={0}
                value={keepDaily}
                onChange={(e) => {
                  setKeepDaily(Number(e.target.value));
                }}
                className="h-8 text-sm"
              />
            </div>
            <div className="space-y-1">
              <Label className="text-xs">Weekly</Label>
              <Input
                type="number"
                min={0}
                value={keepWeekly}
                onChange={(e) => {
                  setKeepWeekly(Number(e.target.value));
                }}
                className="h-8 text-sm"
              />
            </div>
            <div className="space-y-1">
              <Label className="text-xs">Monthly</Label>
              <Input
                type="number"
                min={0}
                value={keepMonthly}
                onChange={(e) => {
                  setKeepMonthly(Number(e.target.value));
                }}
                className="h-8 text-sm"
              />
            </div>
            <div className="space-y-1">
              <Label className="text-xs">Yearly</Label>
              <Input
                type="number"
                min={0}
                value={keepYearly}
                onChange={(e) => {
                  setKeepYearly(Number(e.target.value));
                }}
                className="h-8 text-sm"
              />
            </div>
          </div>

          {!preview && (
            <Button
              onClick={handlePreview}
              disabled={pruneMutation.isPending}
              className="w-full"
              variant="outline"
            >
              {pruneMutation.isPending ? "Loading..." : "Preview"}
            </Button>
          )}

          {preview && (
            <div className="space-y-3">
              <div className="max-h-48 space-y-1 overflow-auto rounded border p-2">
                {preview.map((r) => (
                  <div
                    key={`${r["backup-type"]}-${r["backup-id"]}-${String(r["backup-time"])}`}
                    className={`flex items-center justify-between rounded px-2 py-1 text-xs ${
                      r.keep || r.protected
                        ? "bg-green-500/10 text-green-700 dark:text-green-400"
                        : "bg-red-500/10 text-red-700 dark:text-red-400"
                    }`}
                  >
                    <span className="font-mono">
                      {r["backup-type"]}/{r["backup-id"]}{" "}
                      {formatUnixTime(r["backup-time"])}
                    </span>
                    <span>
                      {r.protected ? (
                        <Badge variant="outline" className="text-[10px]">
                          protected
                        </Badge>
                      ) : r.keep ? (
                        "keep"
                      ) : (
                        "remove"
                      )}
                    </span>
                  </div>
                ))}
              </div>
              <p className="text-xs text-muted-foreground">
                {toKeep.length} kept, {toRemove.length} to remove
              </p>
            </div>
          )}
        </div>
        <DialogFooter>
          <Button
            variant="outline"
            onClick={() => {
              setOpen(false);
              setPreview(null);
            }}
          >
            Cancel
          </Button>
          {preview && (
            <Button
              variant="destructive"
              onClick={handleExecute}
              disabled={pruneMutation.isPending || toRemove.length === 0}
            >
              {pruneMutation.isPending
                ? "Pruning..."
                : `Remove ${toRemove.length} Snapshots`}
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
