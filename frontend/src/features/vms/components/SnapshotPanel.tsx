import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Camera, RotateCcw, Trash2, Loader2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Checkbox } from "@/components/ui/checkbox";
import {
  useSnapshots,
  useCreateSnapshot,
  useDeleteSnapshot,
  useRollbackSnapshot,
} from "../api/vm-queries";
import { TaskProgressBanner } from "./TaskProgressBanner";
import type { ResourceKind } from "../types/vm";

interface SnapshotPanelProps {
  clusterId: string;
  resourceId: string;
  kind: ResourceKind;
}

export function SnapshotPanel({
  clusterId,
  resourceId,
  kind,
}: SnapshotPanelProps) {
  const queryClient = useQueryClient();
  const { data: snapshots, isLoading } = useSnapshots(
    clusterId,
    resourceId,
    kind,
  );
  const createMutation = useCreateSnapshot();
  const deleteMutation = useDeleteSnapshot();
  const rollbackMutation = useRollbackSnapshot();

  const snapshotQueryKey = [
    "clusters",
    clusterId,
    kind === "ct" ? "containers" : "vms",
    resourceId,
    "snapshots",
  ];

  function handleTaskComplete() {
    void queryClient.invalidateQueries({ queryKey: snapshotQueryKey });
    setUpid(null);
  }

  const [showForm, setShowForm] = useState(false);
  const [snapName, setSnapName] = useState("");
  const [description, setDescription] = useState("");
  const [vmstate, setVmstate] = useState(false);
  const [upid, setUpid] = useState<string | null>(null);
  const [confirmDelete, setConfirmDelete] = useState<string | null>(null);
  const [confirmRollback, setConfirmRollback] = useState<string | null>(null);

  function handleCreate(e: React.SyntheticEvent) {
    e.preventDefault();
    createMutation.mutate(
      {
        clusterId,
        resourceId,
        kind,
        body: {
          snap_name: snapName,
          ...(description ? { description } : {}),
          ...(kind === "vm" ? { vmstate } : {}),
        },
      },
      {
        onSuccess: (data) => {
          setUpid(data.upid);
          setShowForm(false);
          setSnapName("");
          setDescription("");
          setVmstate(false);
        },
      },
    );
  }

  function handleDelete(name: string) {
    deleteMutation.mutate(
      { clusterId, resourceId, kind, snapName: name },
      {
        onSuccess: (data) => {
          setUpid(data.upid);
          setConfirmDelete(null);
        },
      },
    );
  }

  function handleRollback(name: string) {
    rollbackMutation.mutate(
      { clusterId, resourceId, kind, snapName: name },
      {
        onSuccess: (data) => {
          setUpid(data.upid);
          setConfirmRollback(null);
        },
      },
    );
  }

  function formatDate(ts: number | undefined): string {
    if (!ts) return "--";
    return new Date(ts * 1000).toLocaleString();
  }

  return (
    <div className="space-y-4">
      {upid && (
        <TaskProgressBanner
          clusterId={clusterId}
          upid={upid}
          kind={kind}
          resourceId={resourceId}
          onComplete={handleTaskComplete}
          description="Snapshot operation"
        />
      )}

      <div className="flex items-center justify-between">
        <h3 className="text-sm font-medium">Snapshots</h3>
        <Button
          size="sm"
          variant="outline"
          className="gap-2"
          onClick={() => {
            setShowForm(!showForm);
          }}
        >
          <Camera className="h-4 w-4" />
          Create Snapshot
        </Button>
      </div>

      {showForm && (
        <form
          onSubmit={handleCreate}
          className="space-y-3 rounded-lg border p-4"
        >
          <div className="grid gap-3 sm:grid-cols-2">
            <div className="space-y-2">
              <Label htmlFor="snap-name">Name</Label>
              <Input
                id="snap-name"
                value={snapName}
                onChange={(e) => {
                  setSnapName(e.target.value);
                }}
                placeholder="e.g. before-upgrade"
                required
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="snap-desc">Description</Label>
              <Input
                id="snap-desc"
                value={description}
                onChange={(e) => {
                  setDescription(e.target.value);
                }}
                placeholder="Optional"
              />
            </div>
          </div>
          {kind === "vm" && (
            <div className="flex items-center gap-2">
              <Checkbox
                id="snap-vmstate"
                checked={vmstate}
                onCheckedChange={(checked) => {
                  setVmstate(Boolean(checked));
                }}
              />
              <Label htmlFor="snap-vmstate" className="text-sm">
                Include RAM state
              </Label>
            </div>
          )}
          {createMutation.isError && (
            <p className="text-sm text-destructive">
              {createMutation.error.message}
            </p>
          )}
          <div className="flex gap-2">
            <Button
              type="submit"
              size="sm"
              disabled={!snapName || createMutation.isPending}
            >
              {createMutation.isPending ? "Creating..." : "Create"}
            </Button>
            <Button
              type="button"
              size="sm"
              variant="outline"
              onClick={() => {
                setShowForm(false);
              }}
            >
              Cancel
            </Button>
          </div>
        </form>
      )}

      {isLoading && (
        <div className="flex items-center justify-center py-8">
          <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
        </div>
      )}

      {!isLoading && snapshots && snapshots.length === 0 && (
        <p className="py-4 text-center text-sm text-muted-foreground">
          No snapshots found.
        </p>
      )}

      {!isLoading && snapshots && snapshots.length > 0 && (
        <div className="overflow-hidden rounded-lg border">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b bg-muted/50">
                <th className="px-4 py-2 text-left font-medium">Name</th>
                <th className="px-4 py-2 text-left font-medium">
                  Description
                </th>
                <th className="px-4 py-2 text-left font-medium">Date</th>
                <th className="px-4 py-2 text-left font-medium">RAM</th>
                <th className="px-4 py-2 text-right font-medium">Actions</th>
              </tr>
            </thead>
            <tbody>
              {snapshots.map((snap) => (
                <tr key={snap.name} className="border-b last:border-b-0">
                  <td className="px-4 py-2 font-medium">{snap.name}</td>
                  <td className="px-4 py-2 text-muted-foreground">
                    {snap.description || "--"}
                  </td>
                  <td className="px-4 py-2 text-muted-foreground">
                    {formatDate(snap.snap_time)}
                  </td>
                  <td className="px-4 py-2 text-muted-foreground">
                    {snap.vmstate ? "Yes" : "No"}
                  </td>
                  <td className="px-4 py-2 text-right">
                    <div className="flex justify-end gap-1">
                      {confirmRollback === snap.name ? (
                        <>
                          <Button
                            size="sm"
                            variant="destructive"
                            onClick={() => {
                              handleRollback(snap.name);
                            }}
                            disabled={rollbackMutation.isPending}
                          >
                            Confirm
                          </Button>
                          <Button
                            size="sm"
                            variant="outline"
                            onClick={() => {
                              setConfirmRollback(null);
                            }}
                          >
                            Cancel
                          </Button>
                        </>
                      ) : (
                        <Button
                          size="sm"
                          variant="ghost"
                          className="gap-1"
                          onClick={() => {
                            setConfirmRollback(snap.name);
                          }}
                        >
                          <RotateCcw className="h-3.5 w-3.5" />
                          Rollback
                        </Button>
                      )}
                      {confirmDelete === snap.name ? (
                        <>
                          <Button
                            size="sm"
                            variant="destructive"
                            onClick={() => {
                              handleDelete(snap.name);
                            }}
                            disabled={deleteMutation.isPending}
                          >
                            Confirm
                          </Button>
                          <Button
                            size="sm"
                            variant="outline"
                            onClick={() => {
                              setConfirmDelete(null);
                            }}
                          >
                            Cancel
                          </Button>
                        </>
                      ) : (
                        <Button
                          size="sm"
                          variant="ghost"
                          className="gap-1 text-destructive hover:text-destructive"
                          onClick={() => {
                            setConfirmDelete(snap.name);
                          }}
                        >
                          <Trash2 className="h-3.5 w-3.5" />
                          Delete
                        </Button>
                      )}
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
