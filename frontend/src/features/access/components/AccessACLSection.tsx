import { useState } from "react";
import { Plus, Trash2 } from "lucide-react";

import { Badge } from "@/components/ui/badge";
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
  DialogDescription,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
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

import {
  type AccessACLEntry,
  type AccessCapabilities,
  useAccessACL,
  useAccessGroups,
  useAccessRoles,
  useAccessUsers,
  useUpdateAccessACL,
} from "../api/access-queries";

interface Props {
  clusterId: string;
  capabilities: AccessCapabilities;
}

type SubjectKind = "user" | "group" | "token";

/** What tells one ACL entry from another: the same four fields its row is keyed on. */
function entryKey(entry: AccessACLEntry): string {
  return `${entry.path}|${entry.type}|${entry.ugid}|${entry.roleid}`;
}

export function AccessACLSection({ clusterId, capabilities }: Props) {
  const { canManage } = useAuth();
  const aclQuery = useAccessACL(clusterId);
  const rolesQuery = useAccessRoles(clusterId);
  const usersQuery = useAccessUsers(clusterId);
  const groupsQuery = useAccessGroups(clusterId);
  const updateACL = useUpdateAccessACL(clusterId);

  const [grantOpen, setGrantOpen] = useState(false);
  const [path, setPath] = useState("/");
  const [role, setRole] = useState("");
  const [subjectKind, setSubjectKind] = useState<SubjectKind>("user");
  const [subject, setSubject] = useState("");
  const [propagate, setPropagate] = useState(true);
  const [error, setError] = useState("");
  // Revoke shares useUpdateAccessACL with grant, so its global toast is
  // suppressed too — this banner is where a failed revoke has to show up.
  const [revokeError, setRevokeError] = useState("");
  const [revokeTarget, setRevokeTarget] = useState<AccessACLEntry | null>(null);

  const manageable = canManage("access") && capabilities.canModifyACL;

  // The hook opts out of the global error toast for grant and revoke alike, so
  // each save settles through its promise: one that settles after the dialog
  // that sent it has gone is toasted, naming what it was, instead of lost (see
  // useSaveOutcome). This section opens each dialog again and again.
  //
  // The grant's is told apart by whether it is open: it is one dialog, and its
  // trigger closes it before anything can open it again. A revoke's confirmation
  // is told apart by WHICH entry's is open, because one can take the place of
  // another with nothing closed between them. The confirmation is not held while
  // the request is out (nor does it hold focus: holdFocusInDialog is not used
  // here), so Tab walks out of it to the rows behind the modal, which a pointer
  // cannot reach, and Enter on another row's Revoke opens that entry's
  // confirmation in place of this one's.
  //
  // A revoke is reported in the banner in the section body as well, but only
  // while its confirmation is still open: the confirmation closes on a failure,
  // which uncovers the banner. For one that was dismissed or replaced while the
  // request was out the banner is no place for it. It names no entry, it sits
  // behind whichever dialog is open next, and the next revoke clears it.
  const settleGrant = useSaveOutcome(grantOpen);
  const settleRevoke = useSaveOutcome(
    revokeTarget === null ? null : entryKey(revokeTarget),
  );

  const subjectField = (kind: SubjectKind): "users" | "groups" | "tokens" =>
    kind === "user" ? "users" : kind === "group" ? "groups" : "tokens";

  const handleGrant = (e: React.SyntheticEvent) => {
    e.preventDefault();
    setError("");
    const onPath = path.trim();
    const to = subject.trim();
    settleGrant(
      updateACL.mutateAsync({
        path: onPath,
        roles: role,
        [subjectField(subjectKind)]: to,
        propagate,
      }),
      {
        action: `Granting ${role} on ${onPath} to ${to}`,
        // Not once the dialog has been dismissed: this would close, and clear,
        // whichever one has been opened in its place.
        onSuccess: () => {
          setGrantOpen(false);
          setSubject("");
        },
        // A dismissal keeps what was filled in, which is this section's and not
        // the dialog's, so a grant that went through after one would leave the
        // subject it has just been made to for the next Grant to repeat. Put it
        // away as a success does, unless a dialog is open: that one is showing
        // the same text as its own.
        onLateSuccess: (_, { open }) => {
          if (!open) setSubject("");
        },
        onError: (err) => {
          setError(
            err instanceof ApiClientError
              ? err.message
              : "Failed to grant access",
          );
        },
      },
    );
  };

  const handleRevoke = (entry: AccessACLEntry) => {
    setRevokeError("");
    const kind =
      entry.type === "group"
        ? "groups"
        : entry.type === "token"
          ? "tokens"
          : "users";
    settleRevoke(
      updateACL.mutateAsync({
        path: entry.path,
        roles: entry.roleid,
        [kind]: entry.ugid,
        delete: true,
      }),
      {
        action: `Revoking ${entry.roleid} on ${entry.path} from ${entry.ugid}`,
        // These run only while the confirmation that sent the request is still
        // the one open — this entry's, not another's that has replaced it — so
        // closing it cannot close another's: useSaveOutcome holds them to that,
        // and nothing here checks it a second time.
        onSuccess: () => {
          setRevokeTarget(null);
        },
        onError: (err) => {
          setRevokeError(
            err instanceof ApiClientError
              ? err.message
              : "Failed to revoke access",
          );
          setRevokeTarget(null);
        },
      },
    );
  };

  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between">
        <div>
          <CardTitle>Permissions</CardTitle>
          {!capabilities.loading && !capabilities.canModifyACL && (
            <p className="mt-1 text-xs text-muted-foreground">
              Read-only: Nexara&apos;s token lacks{" "}
              <code className="font-mono">Permissions.Modify</code>.
            </p>
          )}
        </div>
        {manageable && (
          <Dialog
            open={grantOpen}
            onOpenChange={(open) => {
              setGrantOpen(open);
              // Clear on close: error was only reset at the start of the next
              // submit, so reopening the dialog showed the previous failure.
              if (!open) setError("");
            }}
          >
            <DialogTrigger asChild>
              <Button size="sm">
                <Plus className="mr-2 h-4 w-4" />
                Grant Access
              </Button>
            </DialogTrigger>
            <DialogContent className="max-w-md">
              <DialogHeader>
                <DialogTitle>Grant Access</DialogTitle>
                <DialogDescription>
                  Assigns a role to a user, group or token on an ACL path.
                </DialogDescription>
              </DialogHeader>
              <form onSubmit={handleGrant} className="space-y-4">
                <div>
                  <Label htmlFor="acl-path">Path</Label>
                  <Input
                    id="acl-path"
                    value={path}
                    onChange={(e) => {
                      setPath(e.target.value);
                    }}
                    placeholder="/vms/100"
                    required
                  />
                  <p className="mt-1 text-xs text-muted-foreground">
                    <code className="font-mono">/</code> covers the whole
                    cluster. Others: <code className="font-mono">/vms/100</code>
                    , <code className="font-mono">/storage/local</code>,{" "}
                    <code className="font-mono">/nodes/pve1</code>.
                  </p>
                </div>

                <div>
                  <Label htmlFor="acl-role">Role</Label>
                  <Select value={role} onValueChange={setRole}>
                    <SelectTrigger id="acl-role">
                      <SelectValue placeholder="Select a role" />
                    </SelectTrigger>
                    <SelectContent>
                      {(rolesQuery.data ?? []).map((r) => (
                        <SelectItem key={r.roleid} value={r.roleid}>
                          {r.roleid}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>

                <div>
                  <Label htmlFor="acl-kind">Subject type</Label>
                  <Select
                    value={subjectKind}
                    onValueChange={(v) => {
                      setSubjectKind(v as SubjectKind);
                      setSubject("");
                    }}
                  >
                    <SelectTrigger id="acl-kind">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="user">User</SelectItem>
                      <SelectItem value="group">Group</SelectItem>
                      <SelectItem value="token">API token</SelectItem>
                    </SelectContent>
                  </Select>
                </div>

                <div>
                  <Label htmlFor="acl-subject">
                    {subjectKind === "user"
                      ? "User"
                      : subjectKind === "group"
                        ? "Group"
                        : "Token"}
                  </Label>
                  {subjectKind === "token" ? (
                    <>
                      <Input
                        id="acl-subject"
                        value={subject}
                        onChange={(e) => {
                          setSubject(e.target.value);
                        }}
                        placeholder="user@pve!tokenname"
                        required
                      />
                      <p className="mt-1 text-xs text-muted-foreground">
                        Full token id, including the{" "}
                        <code className="font-mono">!</code>.
                      </p>
                    </>
                  ) : (
                    <Select value={subject} onValueChange={setSubject}>
                      <SelectTrigger id="acl-subject">
                        <SelectValue placeholder={`Select a ${subjectKind}`} />
                      </SelectTrigger>
                      <SelectContent>
                        {subjectKind === "user"
                          ? (usersQuery.data ?? []).map((u) => (
                              <SelectItem key={u.userid} value={u.userid}>
                                {u.userid}
                              </SelectItem>
                            ))
                          : (groupsQuery.data ?? []).map((g) => (
                              <SelectItem key={g.groupid} value={g.groupid}>
                                {g.groupid}
                              </SelectItem>
                            ))}
                      </SelectContent>
                    </Select>
                  )}
                </div>

                <label className="flex items-center gap-2 text-sm">
                  <input
                    type="checkbox"
                    checked={propagate}
                    onChange={(e) => {
                      setPropagate(e.target.checked);
                    }}
                  />
                  Propagate to child paths
                </label>

                {error && <p className="text-sm text-destructive">{error}</p>}

                <Button
                  type="submit"
                  disabled={
                    !path.trim() ||
                    !role ||
                    !subject.trim() ||
                    updateACL.isPending
                  }
                >
                  {updateACL.isPending ? "Granting..." : "Grant"}
                </Button>
              </form>
            </DialogContent>
          </Dialog>
        )}
      </CardHeader>

      <CardContent>
        {revokeError && (
          <div className="mb-3 flex items-start justify-between gap-2 rounded-md border border-destructive/40 bg-destructive/10 p-3">
            <p className="text-sm text-destructive">{revokeError}</p>
            <Button
              variant="ghost"
              size="sm"
              onClick={() => {
                setRevokeError("");
              }}
            >
              Dismiss
            </Button>
          </div>
        )}

        {aclQuery.isLoading ? (
          <Skeleton className="h-24 w-full" />
        ) : !aclQuery.data || aclQuery.data.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            No access control entries.
          </p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Path</TableHead>
                <TableHead>Type</TableHead>
                <TableHead>Subject</TableHead>
                <TableHead>Role</TableHead>
                <TableHead>Propagate</TableHead>
                {manageable && (
                  <TableHead className="text-right">Actions</TableHead>
                )}
              </TableRow>
            </TableHeader>
            <TableBody>
              {aclQuery.data.map((entry) => (
                <TableRow key={entryKey(entry)}>
                  <TableCell className="font-mono text-xs">
                    {entry.path}
                  </TableCell>
                  <TableCell>
                    <Badge variant="secondary">{entry.type}</Badge>
                  </TableCell>
                  <TableCell className="font-mono text-xs">
                    {entry.ugid}
                  </TableCell>
                  <TableCell>{entry.roleid}</TableCell>
                  <TableCell className="text-muted-foreground">
                    {entry.propagate ? "Yes" : "No"}
                  </TableCell>
                  {manageable && (
                    <TableCell className="text-right">
                      <Button
                        variant="ghost"
                        size="sm"
                        aria-label={`Revoke ${entry.roleid} on ${entry.path}`}
                        onClick={() => {
                          setRevokeTarget(entry);
                        }}
                      >
                        <Trash2 className="h-4 w-4 text-destructive" />
                      </Button>
                    </TableCell>
                  )}
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}

        <AlertDialog
          open={revokeTarget !== null}
          onOpenChange={(open) => {
            if (!open) setRevokeTarget(null);
          }}
        >
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle>Revoke access?</AlertDialogTitle>
              <AlertDialogDescription>
                {revokeTarget && (
                  <>
                    <code className="font-mono">{revokeTarget.ugid}</code> loses
                    the <strong>{revokeTarget.roleid}</strong> role on{" "}
                    <code className="font-mono">{revokeTarget.path}</code>,
                    immediately.
                  </>
                )}
              </AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel>Cancel</AlertDialogCancel>
              <AlertDialogAction
                className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
                disabled={updateACL.isPending}
                onClick={(e: React.MouseEvent) => {
                  e.preventDefault();
                  if (revokeTarget) handleRevoke(revokeTarget);
                }}
              >
                {updateACL.isPending ? "Revoking..." : "Revoke"}
              </AlertDialogAction>
            </AlertDialogFooter>
          </AlertDialogContent>
        </AlertDialog>
      </CardContent>
    </Card>
  );
}
