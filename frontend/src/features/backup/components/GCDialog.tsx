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
import { Trash2 } from "lucide-react";
import { useTriggerGC } from "../api/backup-queries";

interface GCDialogProps {
  pbsId: string;
  store: string;
}

export function GCDialog({ pbsId, store }: GCDialogProps) {
  const [open, setOpen] = useState(false);
  const gcMutation = useTriggerGC();

  const handleConfirm = () => {
    gcMutation.mutate(
      { pbsId, store },
      {
        onSuccess: () => {
          setOpen(false);
        },
      },
    );
  };

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button variant="outline" size="sm">
          <Trash2 className="mr-2 h-4 w-4" />
          Run GC
        </Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Run Garbage Collection</DialogTitle>
          <DialogDescription>
            This will start garbage collection on datastore{" "}
            <strong>{store}</strong>. This removes unreferenced chunks and
            reclaims disk space.
          </DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <Button
            variant="outline"
            onClick={() => {
              setOpen(false);
            }}
          >
            Cancel
          </Button>
          <Button onClick={handleConfirm} disabled={gcMutation.isPending}>
            {gcMutation.isPending ? "Starting..." : "Run GC"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
