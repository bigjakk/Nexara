import { useState } from "react";
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
import { Plus } from "lucide-react";
import { PGCalculator } from "./PGCalculator";
import { useCreateCephPool } from "../api/ceph-queries";

interface PoolCreateDialogProps {
  clusterId: string;
  osdCount: number;
}

export function PoolCreateDialog({
  clusterId,
  osdCount,
}: PoolCreateDialogProps) {
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [size, setSize] = useState(3);
  const [pgNum, setPgNum] = useState(128);

  const createPool = useCreateCephPool();

  function handleCreate() {
    createPool.mutate(
      {
        clusterId,
        body: { name, size, pg_num: pgNum },
      },
      {
        onSuccess: () => {
          setOpen(false);
          setName("");
          setSize(3);
          setPgNum(128);
        },
      },
    );
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button size="sm">
          <Plus className="mr-1 h-4 w-4" /> Create Pool
        </Button>
      </DialogTrigger>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>Create Ceph Pool</DialogTitle>
        </DialogHeader>
        <div className="space-y-4">
          <div>
            <Label>Pool Name</Label>
            <Input
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
              <Label>Replication Size</Label>
              <Input
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
              <Label>PG Number</Label>
              <Input
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

          <div className="flex justify-end gap-2">
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
              {createPool.isPending ? "Creating..." : "Create Pool"}
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}
