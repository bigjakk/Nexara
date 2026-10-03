import { Fragment, useState } from "react";
import { ChevronDown, ChevronRight, Pencil, Plus, Trash2 } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { useAuth } from "@/hooks/useAuth";
import { useSaveOutcome } from "@/hooks/useSaveOutcome";
import { ApiClientError } from "@/lib/api-client";
import { unaddressableHint } from "@/lib/api-path";

import {
  type AccessCapabilities,
  useAccessGroup,
  useAccessGroups,
  useCreateAccessGroup,
  useDeleteAccessGroup,
  useUpdateAccessGroup,
} from "../api/access-queries";

interface Props {
  clusterId: string;
  capabilities: AccessCapabilities;
}

export function AccessGroupsSection({ clusterId, capabilities }: Props) {
  const { canManage } = useAuth();
  const groupsQuery = useAccessGroups(clusterId);
  const createGroup = useCreateAccessGroup(clusterId);
  const deleteGroup = useDeleteAccessGroup(clusterId);

  const [expanded, setExpanded] = useState<string | null>(null);
  const [createOpen, setCreateOpen] = useState(false);
  const [groupid, setGroupid] = useState("");
  const [comment, setComment] = useState("");
  const [error, setError] = useState("");
  const [deleteTarget, setDeleteTarget] = useState<string | null>(null);
  const [editTarget, setEditTarget] = useState<{
    groupid: string;
    comment: string;
  } | null>(null);

  const manageable = canManage("access") && capabilities.canModifyUsers;
  const columns = manageable ? 4 : 3;

  // The hook opts out of the global error toast, because the open dialog shows
  // its failure itself; the save settles through its promise, so that one that
  // settles after the dialog has gone is toasted instead of lost (see
  // useSaveOutcome). This section opens the create dialog again and again, so
  // it is told apart by its open flag.
  const settleCreate = useSaveOutcome(createOpen);

  const handleCreate = (e: React.SyntheticEvent) => {
    e.preventDefault();
    setError("");
    const id = groupid.trim();
    settleCreate(
      createGroup.mutateAsync({ groupid: id, ...(comment ? { comment } : {}) }),
      {
        action: `Creating group ${id}`,
        // Not once the dialog has been dismissed: this would close, and clear,
        // whichever one has been opened in its place.
        onSuccess: () => {
          setCreateOpen(false);
          setGroupid("");
          setComment("");
        },
        // A dismissal keeps what was typed, which is this section's and not the
        // dialog's, so a group that was created after one would leave its own
        // id for the next Create to repeat. Put it away as a success does,
        // unless a dialog is open: that one is showing the same text as its own.
        onLateSuccess: (_, { open }) => {
          if (!open) {
            setGroupid("");
            setComment("");
          }
        },
        onError: (err) => {
          setError(
            err instanceof ApiClientError
              ? err.message
              : "Failed to create group",
          );
        },
      },
    );
  };

  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between">
        <CardTitle>Groups</CardTitle>
        {manageable && (
          <Dialog
            open={createOpen}
            onOpenChange={(open) => {
              setCreateOpen(open);
              if (!open) setError("");
            }}
          >
            <DialogTrigger asChild>
              <Button size="sm">
                <Plus className="mr-2 h-4 w-4" />
                Create Group
              </Button>
            </DialogTrigger>
            <DialogContent className="max-w-sm">
              <DialogHeader>
                <DialogTitle>Create Group</DialogTitle>
              </DialogHeader>
              <form onSubmit={handleCreate} className="space-y-4">
                <div>
                  <Label htmlFor="group-id">Group ID</Label>
                  <Input
                    id="group-id"
                    value={groupid}
                    onChange={(e) => {
                      setGroupid(e.target.value);
                    }}
                    required
                  />
                </div>
                <div>
                  <Label htmlFor="group-comment">Comment</Label>
                  <Input
                    id="group-comment"
                    value={comment}
                    onChange={(e) => {
                      setComment(e.target.value);
                    }}
                  />
                </div>
                {error && <p className="text-sm text-destructive">{error}</p>}
                <Button
                  type="submit"
                  disabled={!groupid.trim() || createGroup.isPending}
                >
                  {createGroup.isPending ? "Creating..." : "Create"}
                </Button>
              </form>
            </DialogContent>
          </Dialog>
        )}
      </CardHeader>

      <CardContent>
        {groupsQuery.isLoading ? (
          <Skeleton className="h-20 w-full" />
        ) : !groupsQuery.data || groupsQuery.data.length === 0 ? (
          <p className="text-sm text-muted-foreground">No groups configured.</p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="w-8" />
                <TableHead>Group</TableHead>
                <TableHead>Comment</TableHead>
                {manageable && (
                  <TableHead className="text-right">Actions</TableHead>
                )}
              </TableRow>
            </TableHeader>
            <TableBody>
              {groupsQuery.data.map((group) => {
                const isOpen = expanded === group.groupid;
                return (
                  <Fragment key={group.groupid}>
                    <TableRow
                      className="cursor-pointer"
                      onClick={() => {
                        setExpanded(isOpen ? null : group.groupid);
                      }}
                    >
                      <TableCell>
                        {isOpen ? (
                          <ChevronDown className="h-4 w-4" />
                        ) : (
                          <ChevronRight className="h-4 w-4" />
                        )}
                      </TableCell>
                      <TableCell className="font-medium">
                        {group.groupid}
                      </TableCell>
                      <TableCell className="text-muted-foreground">
                        {group.comment || "—"}
                      </TableCell>
                      {manageable && (
                        <TableCell className="text-right">
                          {/* Proxmox admits a group named "." or "..";
                              Nexara cannot address one (lib/api-path.ts), so
                              the row shows why in place of its actions — as
                              text, since a disabled button's title never
                              shows. */}
                          {unaddressableHint(group.groupid) !== null ? (
                            <span className="text-xs text-muted-foreground">
                              {unaddressableHint(group.groupid)}
                            </span>
                          ) : (
                            <>
                              <Button
                                variant="ghost"
                                size="sm"
                                aria-label={`Edit ${group.groupid}`}
                                onClick={(e) => {
                                  e.stopPropagation();
                                  setEditTarget({
                                    groupid: group.groupid,
                                    comment: group.comment ?? "",
                                  });
                                }}
                              >
                                <Pencil className="h-4 w-4" />
                              </Button>
                              <Button
                                variant="ghost"
                                size="sm"
                                aria-label={`Delete ${group.groupid}`}
                                onClick={(e) => {
                                  e.stopPropagation();
                                  setDeleteTarget(group.groupid);
                                }}
                              >
                                <Trash2 className="h-4 w-4 text-destructive" />
                              </Button>
                            </>
                          )}
                        </TableCell>
                      )}
                    </TableRow>
                    {isOpen && (
                      <TableRow>
                        <TableCell colSpan={columns} className="bg-muted/30">
                          <GroupMembers
                            clusterId={clusterId}
                            groupid={group.groupid}
                          />
                        </TableCell>
                      </TableRow>
                    )}
                  </Fragment>
                );
              })}
            </TableBody>
          </Table>
        )}

        {/* Keyed on the group, so that the dialog of another is a new one: this
            one is not held while its request is out, and Tab walks out of it to
            the Edit buttons behind the modal, which a pointer cannot reach.
            Without the key, Enter on another group's would give it the form
            state, the error and the pending save of the first. */}
        {editTarget && (
          <EditGroupDialog
            key={editTarget.groupid}
            clusterId={clusterId}
            groupid={editTarget.groupid}
            initialComment={editTarget.comment}
            onClose={() => {
              setEditTarget(null);
            }}
          />
        )}

        <AlertDialog
          open={deleteTarget !== null}
          onOpenChange={(open) => {
            if (!open) setDeleteTarget(null);
          }}
        >
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle>Delete group {deleteTarget}?</AlertDialogTitle>
              <AlertDialogDescription>
                Members keep their accounts, but lose any permissions granted
                through this group. This cannot be undone.
              </AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel>Cancel</AlertDialogCancel>
              <AlertDialogAction
                className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
                disabled={deleteGroup.isPending}
                onClick={(e: React.MouseEvent) => {
                  e.preventDefault();
                  if (!deleteTarget) return;
                  const target = deleteTarget;
                  deleteGroup.mutate(target, {
                    // Only this group's confirmation. It is not held while its
                    // request is out, so Tab walks out of it to the Delete
                    // buttons behind the modal, which a pointer cannot reach,
                    // and Enter on another group's opens that one in its place:
                    // this settling must not close it.
                    onSettled: () => {
                      setDeleteTarget((open) =>
                        open === target ? null : open,
                      );
                    },
                  });
                }}
              >
                {deleteGroup.isPending ? "Deleting..." : "Delete Group"}
              </AlertDialogAction>
            </AlertDialogFooter>
          </AlertDialogContent>
        </AlertDialog>
      </CardContent>
    </Card>
  );
}

/**
 * Group members, fetched lazily when the row is expanded — or, for a group
 * Nexara cannot address, why not: reading them needs the same path, which
 * apiPath refuses to build.
 */
function GroupMembers(props: { clusterId: string; groupid: string }) {
  const hint = unaddressableHint(props.groupid);
  if (hint !== null) {
    return <p className="py-1 text-sm text-muted-foreground">{hint}</p>;
  }
  return <AddressableGroupMembers {...props} />;
}

function AddressableGroupMembers({
  clusterId,
  groupid,
}: {
  clusterId: string;
  groupid: string;
}) {
  const groupQuery = useAccessGroup(clusterId, groupid);

  if (groupQuery.isLoading) return <Skeleton className="h-10 w-full" />;

  const members = groupQuery.data?.members ?? [];
  if (members.length === 0) {
    return <p className="py-1 text-sm text-muted-foreground">No members.</p>;
  }

  return (
    <div className="py-1">
      <p className="mb-2 text-sm font-medium">Members</p>
      <div className="flex flex-wrap gap-1">
        {members.map((m) => (
          <code
            key={m}
            className="rounded bg-background px-2 py-1 font-mono text-xs"
          >
            {m}
          </code>
        ))}
      </div>
    </div>
  );
}

/**
 * Edits a group's comment.
 *
 * The comment is always sent, even when unchanged: the API treats an omitted
 * comment as "leave alone" and an empty one as "clear", so a form that only
 * sent non-empty values could never clear a comment.
 */
function EditGroupDialog({
  clusterId,
  groupid,
  initialComment,
  onClose,
}: {
  clusterId: string;
  groupid: string;
  initialComment: string;
  onClose: () => void;
}) {
  const updateGroup = useUpdateAccessGroup(clusterId);
  const [comment, setComment] = useState(initialComment);
  const [error, setError] = useState("");

  // The hook opts out of the global error toast because this dialog shows its
  // own failure, and mutate()'s per-call callbacks do not run once the dialog
  // is gone, so the save settles through its promise (see useSaveOutcome). This
  // component IS the dialog, mounted only while it is open and, being keyed on
  // its group, once for each.
  const settle = useSaveOutcome();

  const handleSave = (e: React.SyntheticEvent) => {
    e.preventDefault();
    setError("");
    settle(updateGroup.mutateAsync({ groupid, comment }), {
      // Named: it can land on another page, or over another group's open Edit
      // dialog.
      action: `Saving group ${groupid}`,
      // Not once dismissed: onClose would close whichever dialog has been
      // opened in this one's place.
      onSuccess: onClose,
      onError: (err) => {
        setError(
          err instanceof ApiClientError
            ? err.message
            : "Failed to update group",
        );
      },
    });
  };

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <DialogContent className="max-w-sm">
        <DialogHeader>
          <DialogTitle>Edit {groupid}</DialogTitle>
        </DialogHeader>
        <form onSubmit={handleSave} className="space-y-4">
          <div>
            <Label htmlFor="edit-group-comment">Comment</Label>
            <Input
              id="edit-group-comment"
              value={comment}
              onChange={(e) => {
                setComment(e.target.value);
              }}
            />
          </div>
          {error && <p className="text-sm text-destructive">{error}</p>}
          <div className="flex justify-end gap-2">
            <Button type="button" variant="outline" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" disabled={updateGroup.isPending}>
              {updateGroup.isPending ? "Saving..." : "Save"}
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}
