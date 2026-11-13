import { useId, useState } from "react";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useChangeBackupOwner } from "../api/backup-queries";
import type { PBSSnapshot } from "../types/backup";

interface ChangeOwnerDialogProps {
  pbsId: string;
  /** Any snapshot of the group: the owner is the group's, not one snapshot's. */
  snapshot: PBSSnapshot;
  /** How many snapshots the group holds, as the list shows them. */
  groupSize: number;
  /**
   * The server's own token: what a storage made with it signs in as, and the
   * only owner filled in, unless it already owns the group. Filling in any
   * other would make one unread click move a group off that token.
   */
  serverToken: string | undefined;
  /** Other owners to offer, as the list shows them. */
  suggestions: readonly string[];
  onClose: () => void;
}

// What the server side asks of an owner: some @ some, no white space.
const OWNER = /^\S+@\S+$/;

/**
 * Gives a backup group to another user or API token. PBS adds a snapshot to a
 * group only for its owner, or the owner's user when the owner is a token, so a
 * storage signing in as anyone else fails with "backup owner check failed".
 */
export function ChangeOwnerDialog({
  pbsId,
  snapshot,
  groupSize,
  serverToken,
  suggestions,
  onClose,
}: ChangeOwnerDialogProps) {
  const changeMutation = useChangeBackupOwner();
  const [owner, setOwner] = useState(() =>
    serverToken !== undefined && serverToken !== snapshot.owner
      ? serverToken
      : "",
  );
  const offered = [
    ...new Set([
      ...(serverToken === undefined ? [] : [serverToken]),
      ...suggestions,
    ]),
  ];
  const inputId = useId();
  const listId = useId();
  const group = `${snapshot.backup_type}/${snapshot.backup_id}`;
  const trimmed = owner.trim();
  const ready = OWNER.test(trimmed) && trimmed !== snapshot.owner;

  const handleSubmit = () => {
    if (!ready) return;
    changeMutation.mutate(
      {
        pbsId,
        store: snapshot.datastore,
        body: {
          backup_type: snapshot.backup_type,
          backup_id: snapshot.backup_id,
          owner: trimmed,
        },
      },
      { onSuccess: onClose },
    );
  };

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Change owner of {group}</DialogTitle>
          <DialogDescription>
            All {String(groupSize)} {groupSize === 1 ? "snapshot" : "snapshots"}{" "}
            of {group} on {snapshot.datastore}, now owned by{" "}
            <span className="font-medium text-foreground">
              {snapshot.owner || "no one"}
            </span>
            .
          </DialogDescription>
        </DialogHeader>
        <p className="text-sm text-muted-foreground">
          PBS adds backups to a group only for its owner, or for the
          owner&apos;s user when the owner is an API token. A storage that signs
          in as anyone else fails with &ldquo;backup owner check failed&rdquo;.
          Give the group to the user or token the storage uses.
        </p>
        <div className="space-y-1.5">
          <Label htmlFor={inputId}>New owner</Label>
          <Input
            id={inputId}
            list={listId}
            value={owner}
            onChange={(e) => {
              setOwner(e.target.value);
            }}
            placeholder="user@realm!token"
            autoComplete="off"
          />
          <datalist id={listId}>
            {offered.map((s) => (
              <option key={s} value={s} />
            ))}
          </datalist>
        </div>
        {changeMutation.error !== null && (
          <p className="text-sm text-destructive">
            {changeMutation.error.message}
          </p>
        )}
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button
            onClick={handleSubmit}
            disabled={!ready || changeMutation.isPending}
          >
            {changeMutation.isPending ? "Changing..." : "Change owner"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
