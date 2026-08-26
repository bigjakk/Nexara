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
import { ApiClientError } from "@/lib/api-client";

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
  const [editTarget, setEditTarget] = useState<{ groupid: string; comment: string } | null>(null);

  const manageable = canManage("access") && capabilities.canModifyUsers;
  const columns = manageable ? 4 : 3;

  const handleCreate = (e: React.SyntheticEvent) => {
    e.preventDefault();
    setError("");
    createGroup.mutate(
      { groupid: groupid.trim(), ...(comment ? { comment } : {}) },
      {
        onSuccess: () => {
          setCreateOpen(false);
          setGroupid("");
          setComment("");
        },
        onError: (err) => {
          setError(err instanceof ApiClientError ? err.message : "Failed to create group");
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
                    onChange={(e) => { setGroupid(e.target.value); }}
                    required
                  />
                </div>
                <div>
                  <Label htmlFor="group-comment">Comment</Label>
                  <Input
                    id="group-comment"
                    value={comment}
                    onChange={(e) => { setComment(e.target.value); }}
                  />
                </div>
                {error && <p className="text-sm text-destructive">{error}</p>}
                <Button type="submit" disabled={!groupid.trim() || createGroup.isPending}>
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
                {manageable && <TableHead className="text-right">Actions</TableHead>}
              </TableRow>
            </TableHeader>
            <TableBody>
              {groupsQuery.data.map((group) => {
                const isOpen = expanded === group.groupid;
                return (
                  <Fragment key={group.groupid}>
                    <TableRow
                      className="cursor-pointer"
                      onClick={() => { setExpanded(isOpen ? null : group.groupid); }}
                    >
                      <TableCell>
                        {isOpen ? <ChevronDown className="h-4 w-4" /> : <ChevronRight className="h-4 w-4" />}
                      </TableCell>
                      <TableCell className="font-medium">{group.groupid}</TableCell>
                      <TableCell className="text-muted-foreground">{group.comment || "—"}</TableCell>
                      {manageable && (
                        <TableCell className="text-right">
                          <Button
                            variant="ghost"
                            size="sm"
                            aria-label={`Edit ${group.groupid}`}
                            onClick={(e) => {
                              e.stopPropagation();
                              setEditTarget({ groupid: group.groupid, comment: group.comment ?? "" });
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
                        </TableCell>
                      )}
                    </TableRow>
                    {isOpen && (
                      <TableRow>
                        <TableCell colSpan={columns} className="bg-muted/30">
                          <GroupMembers clusterId={clusterId} groupid={group.groupid} />
                        </TableCell>
                      </TableRow>
                    )}
                  </Fragment>
                );
              })}
            </TableBody>
          </Table>
        )}

        {editTarget && (
          <EditGroupDialog
            clusterId={clusterId}
            groupid={editTarget.groupid}
            initialComment={editTarget.comment}
            onClose={() => { setEditTarget(null); }}
          />
        )}

        <AlertDialog
          open={deleteTarget !== null}
          onOpenChange={(open) => { if (!open) setDeleteTarget(null); }}
        >
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle>Delete group {deleteTarget}?</AlertDialogTitle>
              <AlertDialogDescription>
                Members keep their accounts, but lose any permissions granted through this
                group. This cannot be undone.
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
                  deleteGroup.mutate(deleteTarget, {
                    onSettled: () => { setDeleteTarget(null); },
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

/** Group members, fetched lazily when the row is expanded. */
function GroupMembers({ clusterId, groupid }: { clusterId: string; groupid: string }) {
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
          <code key={m} className="rounded bg-background px-2 py-1 font-mono text-xs">
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

  const handleSave = (e: React.SyntheticEvent) => {
    e.preventDefault();
    setError("");
    updateGroup.mutate(
      { groupid, comment },
      {
        onSuccess: onClose,
        onError: (err) => {
          setError(err instanceof ApiClientError ? err.message : "Failed to update group");
        },
      },
    );
  };

  return (
    <Dialog open onOpenChange={(open) => { if (!open) onClose(); }}>
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
              onChange={(e) => { setComment(e.target.value); }}
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
