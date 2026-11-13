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

/** The five retention counts, in the order PBS applies them. */
const KEEP_FIELDS = [
  { id: "last", label: "Last" },
  { id: "daily", label: "Daily" },
  { id: "weekly", label: "Weekly" },
  { id: "monthly", label: "Monthly" },
  { id: "yearly", label: "Yearly" },
] as const;

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

  const keep = {
    last: keepLast,
    daily: keepDaily,
    weekly: keepWeekly,
    monthly: keepMonthly,
    yearly: keepYearly,
  };
  const keepSetters = {
    last: setKeepLast,
    daily: setKeepDaily,
    weekly: setKeepWeekly,
    monthly: setKeepMonthly,
    yearly: setKeepYearly,
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
        <Button variant="outline" size="sm" className="gap-1.5">
          <Scissors className="h-3.5 w-3.5" />
          Prune
        </Button>
      </DialogTrigger>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>Prune {store}</DialogTitle>
          <DialogDescription>
            Set how many snapshots to keep, then preview which ones would be
            removed.
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-4 py-2">
          <fieldset>
            <legend className="pb-2 text-sm font-medium">Keep</legend>
            <div className="grid grid-cols-[repeat(auto-fit,minmax(4.5rem,1fr))] gap-3">
              {KEEP_FIELDS.map((field) => (
                <div key={field.id} className="space-y-1">
                  <Label
                    htmlFor={`prune-${field.id}`}
                    className="text-xs text-muted-foreground"
                  >
                    {field.label}
                  </Label>
                  <Input
                    id={`prune-${field.id}`}
                    type="number"
                    min={0}
                    value={keep[field.id]}
                    onChange={(e) => {
                      keepSetters[field.id](Number(e.target.value));
                    }}
                    className="h-8 text-sm"
                  />
                </div>
              ))}
            </div>
          </fieldset>

          {preview && (
            <div className="space-y-2">
              <p className="text-sm">
                <span className="font-medium">{toRemove.length} to remove</span>
                <span className="text-muted-foreground">
                  , {toKeep.length} kept
                </span>
              </p>
              <ul className="max-h-56 divide-y overflow-auto rounded-[7px] bg-foreground/[0.04] px-3">
                {preview.map((r) => (
                  <li
                    key={`${r["backup-type"]}-${r["backup-id"]}-${String(r["backup-time"])}`}
                    className="flex items-center justify-between gap-3 py-1.5 text-xs"
                  >
                    <span className="font-mono">
                      {r["backup-type"]}/{r["backup-id"]}{" "}
                      <span className="text-muted-foreground">
                        {formatUnixTime(r["backup-time"])}
                      </span>
                    </span>
                    {r.protected ? (
                      <span className="text-muted-foreground">protected</span>
                    ) : r.keep ? (
                      <span className="text-muted-foreground">keep</span>
                    ) : (
                      <span className="font-medium text-red-700 dark:text-red-400">
                        remove
                      </span>
                    )}
                  </li>
                ))}
              </ul>
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
          {!preview && (
            <Button onClick={handlePreview} disabled={pruneMutation.isPending}>
              {pruneMutation.isPending ? "Loading..." : "Preview"}
            </Button>
          )}
          {preview && (
            <Button
              variant="destructive"
              onClick={handleExecute}
              disabled={pruneMutation.isPending || toRemove.length === 0}
            >
              {pruneMutation.isPending
                ? "Pruning..."
                : `Remove ${String(toRemove.length)} snapshots`}
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
