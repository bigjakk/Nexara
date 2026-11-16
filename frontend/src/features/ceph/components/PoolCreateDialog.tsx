import { useState } from "react";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Plus } from "lucide-react";
import { PGCalculator } from "./PGCalculator";
import { useCreateCephPool } from "../api/ceph-queries";
import { useSaveOutcome } from "@/hooks/useSaveOutcome";
import { useTranslation } from "react-i18next";

interface PoolCreateDialogProps {
  clusterId: string;
  osdCount: number;
}

export function PoolCreateDialog({
  clusterId,
  osdCount,
}: PoolCreateDialogProps) {
  const { t } = useTranslation("ceph");
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [size, setSize] = useState(3);
  const [pgNum, setPgNum] = useState(128);

  const createPool = useCreateCephPool();
  const settle = useSaveOutcome(open);

  function handleCreate() {
    // The dialog stays mounted while closed, so a create that lands after it
    // closed clears the form too.
    const clear = () => {
      setName("");
      setSize(3);
      setPgNum(128);
    };
    settle(
      createPool.mutateAsync({
        clusterId,
        body: { name, size, pg_num: pgNum },
      }),
      {
        action: t("poolCreateDialogActions.creatingPoolName", { name }),
        onSuccess: () => {
          setOpen(false);
          clear();
        },
        onLateSuccess: (_result, now) => {
          if (!now.open) clear();
        },
        onError: () => undefined, // shown below from the mutation's state
      },
    );
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button size="sm">
          <Plus className="h-4 w-4" />
          Create pool
        </Button>
      </DialogTrigger>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>Create Ceph pool</DialogTitle>
        </DialogHeader>
        <div className="space-y-4">
          <div>
            <Label htmlFor="ceph-pool-name">Pool name</Label>
            <Input
              id="ceph-pool-name"
              value={name}
              onChange={(e) => {
                setName(e.target.value);
              }}
              placeholder="my-pool"
              className="mt-1"
            />
          </div>
          <div className="grid grid-cols-2 gap-4">
            <div>
              <Label htmlFor="ceph-pool-size">Replicas</Label>
              <Input
                id="ceph-pool-size"
                type="number"
                value={size}
                min={1}
                max={10}
                onChange={(e) => {
                  setSize(Number(e.target.value) || 3);
                }}
                className="mt-1"
              />
            </div>
            <div>
              <Label htmlFor="ceph-pool-pgs">Placement groups</Label>
              <Input
                id="ceph-pool-pgs"
                type="number"
                value={pgNum}
                min={1}
                onChange={(e) => {
                  setPgNum(Number(e.target.value) || 128);
                }}
                className="mt-1"
              />
            </div>
          </div>

          <PGCalculator osdCount={osdCount} onPGNumChange={setPgNum} />

          {createPool.isError && (
            <p className="text-sm text-destructive">
              {createPool.error.message}
            </p>
          )}
        </div>
        <DialogFooter>
          <Button
            variant="outline"
            onClick={() => {
              setOpen(false);
            }}
          >
            Cancel
          </Button>
          <Button
            onClick={handleCreate}
            disabled={!name || createPool.isPending}
          >
            {createPool.isPending ? "Creating..." : "Create pool"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
