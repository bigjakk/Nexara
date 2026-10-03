import { Fragment, useState } from "react";
import {
  AlertTriangle,
  ChevronDown,
  ChevronRight,
  KeyRound,
  Pencil,
  Plus,
  RefreshCw,
  Trash2,
} from "lucide-react";
import { toast } from "sonner";

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
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
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
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { ApiClientError } from "@/lib/api-client";
import { useAuth } from "@/hooks/useAuth";
import { useSaveOutcome } from "@/hooks/useSaveOutcome";

import {
  type AccessCapabilities,
  type AccessTokenCreated,
  type AccessUser,
  type UpdateAccessUserInput,
  useAccessTokens,
  useAccessUser,
  useAccessUsers,
  useCreateAccessToken,
  useCreateAccessUser,
  useDeleteAccessToken,
  useDeleteAccessUser,
  useUpdateAccessToken,
  useUpdateAccessUser,
} from "../api/access-queries";
import { TokenSecretDialog } from "./TokenSecretDialog";
import { SelfCredentialConfirm } from "./SelfCredentialConfirm";
import { holdFocusInDialog } from "./holdFocusInDialog";

interface Props {
  clusterId: string;
  capabilities: AccessCapabilities;
}

/** Renders a PVE expiry timestamp; 0 or absent means "never". */
function expiryLabel(expire?: number): string {
  if (!expire) return "Never";
  return new Date(expire * 1000).toLocaleDateString();
}

function errorMessage(err: unknown, fallback: string): string {
  return err instanceof ApiClientError ? err.message : fallback;
}

/**
 * Whether the server refused an action because it would cut Nexara off from the
 * cluster: a 409 from guardSelfCredential, which says why and how to go ahead
 * anyway (force=true). Every flow it can refuse answers it with a
 * type-to-confirm override, which sends the action again with force, and
 * decides to open that override with this.
 */
function isOverridable(err: unknown): err is ApiClientError {
  return err instanceof ApiClientError && err.status === 409;
}

/**
 * What a refusal that has an override says when there is no one left to give
 * it: the dialog is gone. The server's words end with "Retry with force=true to
 * proceed anyway", which is for an API caller and cannot be acted on from a
 * toast, so they are not used: this says what happened to the action, and what
 * to do to go ahead.
 */
function refusedBeyondOverride(action: string): string {
  return `${action} was refused, and nothing was changed, because it could cut Nexara off from the cluster. To go ahead anyway, do it again and confirm the override that is then offered.`;
}

/**
 * How long a notice that a token's secret could not be shown stays up. Long,
 * because it is the one thing such a toast has to be read for: Proxmox shows a
 * secret once, so the operator has to regenerate the token, and after a
 * regenerate the old secret is already dead. The default of a few seconds is
 * easily missed by someone who has just navigated away from the page that would
 * have shown it. (sonner pauses a toast under the pointer, and the Toaster has
 * a close button.)
 *
 * Finite, all the same. It is about a moment: once the operator has
 * regenerated the token, or given up on it, a notice that is still there
 * misleads, and nothing takes it down but them. And it does not have to outlive
 * its session to be wrong. A sign-out and a change of user dismiss every toast
 * (stores/session-reset.ts, auth-store), and useSaveOutcome raises none for a
 * session that has ended, so this one cannot reach the next user's Toaster. But
 * that dismissal only reports a failure of sonner's and carries on, and a bound
 * keeps a notice that it did not reach from lasting for ever.
 */
const SECRET_NOTICE_MS = 30_000;

/**
 * A failure banner rendered in the section body rather than inside a dialog.
 *
 * These mutations opt out of the global error toast, so wherever their errors
 * are shown has to be visible at the moment they happen. An earlier version
 * wrote delete failures into the create-user dialog's error slot, which meant a
 * failed delete showed nothing at all — and then surfaced, misattributed, the
 * next time the operator opened the create form.
 */
function ErrorBanner({
  message,
  onDismiss,
}: {
  message: string;
  onDismiss: () => void;
}) {
  return (
    <div className="mb-3 flex items-start justify-between gap-2 rounded-md border border-destructive/40 bg-destructive/10 p-3">
      <div className="flex items-start gap-2">
        <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-destructive" />
        <p className="text-sm text-destructive">{message}</p>
      </div>
      <Button variant="ghost" size="sm" onClick={onDismiss}>
        Dismiss
      </Button>
    </div>
  );
}

export function AccessUsersSection({ clusterId, capabilities }: Props) {
  const { canManage } = useAuth();
  const usersQuery = useAccessUsers(clusterId);
  const createUser = useCreateAccessUser(clusterId);
  const deleteUser = useDeleteAccessUser(clusterId);

  const [expanded, setExpanded] = useState<string | null>(null);
  const [createOpen, setCreateOpen] = useState(false);
  const [userid, setUserid] = useState("");
  const [comment, setComment] = useState("");
  const [password, setPassword] = useState("");
  const [createError, setCreateError] = useState("");

  // Rendered in the card body — see ErrorBanner.
  const [sectionError, setSectionError] = useState("");
  const [deleteTarget, setDeleteTarget] = useState<AccessUser | null>(null);
  const [editTarget, setEditTarget] = useState<string | null>(null);
  // Set when the server refuses a DELETE because the target is Nexara's own
  // credential, and its override force-deletes. An edit's refusal must never
  // land here: EditUserDialog keeps its own override, which forces the edit.
  const [deleteConflict, setDeleteConflict] = useState<{
    message: string;
    userid: string;
  } | null>(null);

  const manageable = canManage("access") && capabilities.canModifyUsers;
  const columns = manageable ? 6 : 5;

  // Both hooks opt out of the global error toast, because what sent a save
  // shows its failure itself; these settle each save through its promise, so
  // that one that settles after that has gone is toasted instead of lost (see
  // useSaveOutcome). The create dialog is one of several this section opens, so
  // it is told apart by its open flag; the delete's confirmations are held
  // while the request is out (below), and only the page being left takes them
  // away.
  const settleCreate = useSaveOutcome(createOpen);
  const settleDelete = useSaveOutcome();

  const resetCreate = () => {
    setUserid("");
    setComment("");
    setPassword("");
    setCreateError("");
  };

  const handleCreate = (e: React.SyntheticEvent) => {
    e.preventDefault();
    setCreateError("");
    const id = userid.trim();
    settleCreate(
      createUser.mutateAsync({
        userid: id,
        ...(comment ? { comment } : {}),
        ...(password ? { password } : {}),
      }),
      {
        action: `Creating user ${id}`,
        // Not once the dialog has been dismissed: this would close, and clear,
        // whichever one has been opened in its place.
        onSuccess: () => {
          setCreateOpen(false);
          resetCreate();
        },
        onError: (err) => {
          setCreateError(errorMessage(err, "Failed to create user"));
        },
      },
    );
  };

  const handleDelete = (target: string, force: boolean) => {
    setSectionError("");
    const action = `Deleting user ${target}`;
    settleDelete(deleteUser.mutateAsync({ userid: target, force }), {
      action,
      lateFailure: (err) =>
        isOverridable(err) ? refusedBeyondOverride(action) : undefined,
      onSuccess: () => {
        setDeleteConflict(null);
        setDeleteTarget(null);
      },
      onError: (err) => {
        setDeleteTarget(null);
        // 409 means the target is the credential Nexara authenticates with.
        // Hand it to the type-to-confirm override rather than reporting a
        // plain failure — it is a refusal, not an error.
        if (isOverridable(err)) {
          setDeleteConflict({ message: err.message, userid: target });
          return;
        }
        setDeleteConflict(null);
        setSectionError(errorMessage(err, "Failed to delete user"));
      },
    });
  };

  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between">
        <CardTitle>Proxmox Users</CardTitle>
        {manageable && (
          <Dialog
            open={createOpen}
            onOpenChange={(open) => {
              setCreateOpen(open);
              if (!open) resetCreate();
            }}
          >
            <DialogTrigger asChild>
              <Button size="sm">
                <Plus className="mr-2 h-4 w-4" />
                Create User
              </Button>
            </DialogTrigger>
            <DialogContent className="max-w-sm">
              <DialogHeader>
                <DialogTitle>Create Proxmox User</DialogTitle>
                <DialogDescription>
                  Users are created on the cluster itself, not in Nexara.
                </DialogDescription>
              </DialogHeader>
              <form onSubmit={handleCreate} className="space-y-4">
                <div>
                  <Label htmlFor="access-userid">User ID</Label>
                  <Input
                    id="access-userid"
                    value={userid}
                    onChange={(e) => {
                      setUserid(e.target.value);
                    }}
                    placeholder="automation@pve"
                    required
                  />
                  <p className="mt-1 text-xs text-muted-foreground">
                    Must be <code className="font-mono">name@realm</code>.
                  </p>
                </div>
                <div>
                  <Label htmlFor="access-comment">Comment</Label>
                  <Input
                    id="access-comment"
                    value={comment}
                    onChange={(e) => {
                      setComment(e.target.value);
                    }}
                  />
                </div>
                <div>
                  <Label htmlFor="access-password">Password</Label>
                  <Input
                    id="access-password"
                    type="password"
                    value={password}
                    onChange={(e) => {
                      setPassword(e.target.value);
                    }}
                    autoComplete="new-password"
                  />
                  <p className="mt-1 text-xs text-muted-foreground">
                    Leave empty for a token-only account that cannot log in
                    interactively.
                  </p>
                </div>
                {createError && (
                  <p className="text-sm text-destructive">{createError}</p>
                )}
                <Button
                  type="submit"
                  disabled={!userid.trim() || createUser.isPending}
                >
                  {createUser.isPending ? "Creating..." : "Create"}
                </Button>
              </form>
            </DialogContent>
          </Dialog>
        )}
      </CardHeader>

      <CardContent>
        {sectionError && (
          <ErrorBanner
            message={sectionError}
            onDismiss={() => {
              setSectionError("");
            }}
          />
        )}

        {usersQuery.isLoading ? (
          <Skeleton className="h-24 w-full" />
        ) : !usersQuery.data || usersQuery.data.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            No Proxmox users found.
          </p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="w-8" />
                <TableHead>User ID</TableHead>
                <TableHead>Status</TableHead>
                <TableHead>Groups</TableHead>
                <TableHead>Comment</TableHead>
                {manageable && (
                  <TableHead className="text-right">Actions</TableHead>
                )}
              </TableRow>
            </TableHeader>
            <TableBody>
              {usersQuery.data.map((user: AccessUser) => {
                const isOpen = expanded === user.userid;
                return (
                  <Fragment key={user.userid}>
                    <TableRow
                      className="cursor-pointer"
                      onClick={() => {
                        setExpanded(isOpen ? null : user.userid);
                      }}
                    >
                      <TableCell>
                        {isOpen ? (
                          <ChevronDown className="h-4 w-4" />
                        ) : (
                          <ChevronRight className="h-4 w-4" />
                        )}
                      </TableCell>
                      <TableCell className="font-mono text-xs">
                        {user.userid}
                      </TableCell>
                      <TableCell>
                        <Badge
                          variant={
                            user.enable === false ? "secondary" : "default"
                          }
                        >
                          {user.enable === false ? "Disabled" : "Enabled"}
                        </Badge>
                      </TableCell>
                      <TableCell className="text-muted-foreground">
                        {user.groups || "—"}
                      </TableCell>
                      <TableCell className="text-muted-foreground">
                        {user.comment || "—"}
                      </TableCell>
                      {manageable && (
                        <TableCell className="text-right">
                          <Button
                            variant="ghost"
                            size="sm"
                            aria-label={`Edit ${user.userid}`}
                            onClick={(e) => {
                              e.stopPropagation();
                              setEditTarget(user.userid);
                            }}
                          >
                            <Pencil className="h-4 w-4" />
                          </Button>
                          <Button
                            variant="ghost"
                            size="sm"
                            aria-label={`Delete ${user.userid}`}
                            onClick={(e) => {
                              e.stopPropagation();
                              setDeleteTarget(user);
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
                          <UserTokens
                            clusterId={clusterId}
                            userid={user.userid}
                            manageable={manageable}
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

        {/* Deleting a PVE user takes every API token it owns with it, and
            Proxmox cannot recreate those secrets — so this confirms, like the
            less destructive group and role deletes already do.

            It is held open while the DELETE is in flight. Closing it would not
            recall the request, and the refusal, arriving after the operator
            had moved on, would open the delete's override over whatever they
            were doing — a prompt to force-delete a user has been seen over
            that user's edit form. Held, the refusal lands on the dialog it
            answers. */}
        <AlertDialog
          open={deleteTarget !== null}
          onOpenChange={(open) => {
            if (!open && !deleteUser.isPending) setDeleteTarget(null);
          }}
        >
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle>
                Delete {deleteTarget?.userid}?
              </AlertDialogTitle>
              <AlertDialogDescription>
                This removes the Proxmox user and every API token it owns.
                Anything authenticating with one of those tokens loses access
                immediately, and the secrets cannot be recovered. This cannot be
                undone.
              </AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel disabled={deleteUser.isPending}>
                Cancel
              </AlertDialogCancel>
              <AlertDialogAction
                className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
                disabled={deleteUser.isPending}
                onClick={(e: React.MouseEvent) => {
                  e.preventDefault();
                  if (deleteTarget) {
                    handleDelete(deleteTarget.userid, false);
                    holdFocusInDialog(e);
                  }
                }}
              >
                {deleteUser.isPending ? "Deleting..." : "Delete User"}
              </AlertDialogAction>
            </AlertDialogFooter>
          </AlertDialogContent>
        </AlertDialog>

        {/* Keyed on the account, so the form state is always that account's:
            what was typed, the error, and the refused edit its override would
            force can never carry over to another. */}
        {editTarget !== null && (
          <EditUserDialog
            key={editTarget}
            clusterId={clusterId}
            userid={editTarget}
            onClose={() => {
              setEditTarget(null);
            }}
          />
        )}

        {deleteConflict && (
          <SelfCredentialConfirm
            // A refusal for another user is a new dialog: nothing typed for the
            // last one carries over.
            key={deleteConflict.userid}
            title="Deleting this user will cut off Nexara's access"
            message={deleteConflict.message}
            confirmValue={deleteConflict.userid}
            actionLabel="Delete User"
            pending={deleteUser.isPending}
            onCancel={() => {
              setDeleteConflict(null);
            }}
            onConfirm={() => {
              handleDelete(deleteConflict.userid, true);
            }}
          />
        )}
      </CardContent>
    </Card>
  );
}

/**
 * What each token override says. Its title names the action it confirms and its
 * button is that action's verb; kept as pairs so a title and its button cannot
 * be crossed. Which request the button sends is chosen in onConfirm.
 */
const TOKEN_OVERRIDE = {
  revoke: {
    title: "Revoking this token will cut off Nexara's access",
    actionLabel: "Revoke Token",
  },
  regenerate: {
    title: "Regenerating this token will cut off Nexara's access",
    actionLabel: "Regenerate Token",
  },
} as const;

/**
 * The API tokens belonging to one user, rendered inside its expanded row.
 *
 * Tokens are fetched lazily on expand rather than prefetched for every user:
 * each is a separate upstream call, and a cluster can have many users.
 */
function UserTokens({
  clusterId,
  userid,
  manageable,
}: {
  clusterId: string;
  userid: string;
  manageable: boolean;
}) {
  const tokensQuery = useAccessTokens(clusterId, userid);
  const createToken = useCreateAccessToken(clusterId);
  const updateToken = useUpdateAccessToken(clusterId);
  const deleteToken = useDeleteAccessToken(clusterId);

  const [tokenName, setTokenName] = useState("");
  const [tokenComment, setTokenComment] = useState("");
  const [privsep, setPrivsep] = useState(true);
  const [error, setError] = useState("");
  const [minted, setMinted] = useState<AccessTokenCreated | null>(null);
  const [revokeTarget, setRevokeTarget] = useState<string | null>(null);
  const [regenTarget, setRegenTarget] = useState<string | null>(null);
  // Carries the full "user@realm!name" so the confirm matches what is shown.
  const [selfConflict, setSelfConflict] = useState<{
    message: string;
    tokenid: string;
    action: "revoke" | "regenerate";
  } | null>(null);

  // These three hooks opt out of the global error toast, so each save settles
  // through its promise (see useSaveOutcome): a failure that comes after this
  // row has gone — collapsed, or the page left — is toasted instead of lost.
  // The row is the whole of what sent them, and the confirmations are held
  // while their request is out, so there is no dialog to tell apart.
  const settle = useSaveOutcome();

  const fullTokenId = (tokenid: string) => `${userid}!${tokenid}`;

  // A secret is shown once, by the dialog this row raises when the answer
  // arrives, and Proxmox keeps no copy to show again. An answer that arrives
  // after the row has gone has nowhere to show it, so the operator is told what
  // became of the token instead — never the secret itself, which a toast would
  // leave on screen for anyone who looks. (And not at all once the session has
  // ended: useSaveOutcome stays silent then, so a secret never reaches the next
  // user's screen.)
  const secretNotShown = (tokenid: string, regenerated: boolean) => {
    const token = fullTokenId(tokenid);
    toast.error(
      regenerated
        ? `Regenerated the API token ${token}, but its new secret could not be shown because this view was closed. The old secret no longer works, and Proxmox shows a secret only once: regenerate the token again to get a new one.`
        : `Created the API token ${token}, but its secret could not be shown because this view was closed. Proxmox shows a secret only once: regenerate the token to get a new one.`,
      { duration: SECRET_NOTICE_MS },
    );
  };

  const handleCreate = (e: React.SyntheticEvent) => {
    e.preventDefault();
    setError("");
    const name = tokenName.trim();
    settle(
      createToken.mutateAsync({
        userid,
        tokenid: name,
        privsep,
        ...(tokenComment ? { comment: tokenComment } : {}),
      }),
      {
        action: `Creating token ${fullTokenId(name)}`,
        onSuccess: (created) => {
          setMinted(created);
          setTokenName("");
          setTokenComment("");
          setPrivsep(true);
        },
        onError: (err) => {
          setError(errorMessage(err, "Failed to create token"));
        },
        onLateSuccess: () => {
          secretNotShown(name, false);
        },
      },
    );
  };

  const handleRevoke = (tokenid: string, force: boolean) => {
    setError("");
    const action = `Revoking token ${fullTokenId(tokenid)}`;
    settle(deleteToken.mutateAsync({ userid, tokenid, force }), {
      action,
      lateFailure: (err) =>
        isOverridable(err) ? refusedBeyondOverride(action) : undefined,
      onSuccess: () => {
        setSelfConflict(null);
        setRevokeTarget(null);
      },
      onError: (err) => {
        setRevokeTarget(null);
        if (isOverridable(err)) {
          setSelfConflict({
            message: err.message,
            tokenid,
            action: "revoke",
          });
          return;
        }
        setSelfConflict(null);
        setError(errorMessage(err, "Failed to revoke token"));
      },
    });
  };

  const handleRegenerate = (tokenid: string, force: boolean) => {
    setError("");
    const action = `Regenerating token ${fullTokenId(tokenid)}`;
    settle(
      updateToken.mutateAsync({ userid, tokenid, regenerate: true, force }),
      {
        action,
        lateFailure: (err) =>
          isOverridable(err) ? refusedBeyondOverride(action) : undefined,
        onSuccess: (updated) => {
          setSelfConflict(null);
          setRegenTarget(null);
          setMinted(updated);
        },
        onError: (err) => {
          setRegenTarget(null);
          if (isOverridable(err)) {
            setSelfConflict({
              message: err.message,
              tokenid,
              action: "regenerate",
            });
            return;
          }
          setSelfConflict(null);
          setError(errorMessage(err, "Failed to regenerate token"));
        },
        // The old secret stopped working when the new one was issued.
        onLateSuccess: () => {
          secretNotShown(tokenid, true);
        },
      },
    );
  };

  return (
    <div className="space-y-3 py-1">
      <div className="flex items-center gap-2 text-sm font-medium">
        <KeyRound className="h-4 w-4" />
        API Tokens
      </div>

      {tokensQuery.isLoading ? (
        <Skeleton className="h-12 w-full" />
      ) : !tokensQuery.data || tokensQuery.data.length === 0 ? (
        <p className="text-sm text-muted-foreground">
          No API tokens for this user.
        </p>
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Token</TableHead>
              <TableHead>Privilege Separation</TableHead>
              <TableHead>Expires</TableHead>
              <TableHead>Comment</TableHead>
              {manageable && (
                <TableHead className="text-right">Actions</TableHead>
              )}
            </TableRow>
          </TableHeader>
          <TableBody>
            {tokensQuery.data.map((token) => (
              <TableRow key={token.tokenid}>
                <TableCell className="font-mono text-xs">
                  {fullTokenId(token.tokenid)}
                </TableCell>
                <TableCell>
                  <Badge variant={token.privsep ? "default" : "destructive"}>
                    {token.privsep ? "Separated" : "Full user privileges"}
                  </Badge>
                </TableCell>
                <TableCell className="text-muted-foreground">
                  {expiryLabel(token.expire)}
                </TableCell>
                <TableCell className="text-muted-foreground">
                  {token.comment || "—"}
                </TableCell>
                {manageable && (
                  <TableCell className="text-right">
                    <Button
                      variant="ghost"
                      size="sm"
                      aria-label={`Regenerate ${token.tokenid}`}
                      onClick={() => {
                        setRegenTarget(token.tokenid);
                      }}
                    >
                      <RefreshCw className="h-4 w-4" />
                    </Button>
                    <Button
                      variant="ghost"
                      size="sm"
                      aria-label={`Revoke ${token.tokenid}`}
                      onClick={() => {
                        setRevokeTarget(token.tokenid);
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

      {manageable && (
        <form
          onSubmit={handleCreate}
          className="flex flex-wrap items-end gap-2 pt-1"
        >
          <div className="min-w-40">
            <Label htmlFor={`token-name-${userid}`} className="text-xs">
              New token name
            </Label>
            <Input
              id={`token-name-${userid}`}
              value={tokenName}
              onChange={(e) => {
                setTokenName(e.target.value);
              }}
              placeholder="automation"
              className="h-8"
            />
          </div>
          <div className="min-w-40">
            <Label htmlFor={`token-comment-${userid}`} className="text-xs">
              Comment
            </Label>
            <Input
              id={`token-comment-${userid}`}
              value={tokenComment}
              onChange={(e) => {
                setTokenComment(e.target.value);
              }}
              className="h-8"
            />
          </div>
          <label className="flex items-center gap-2 pb-1 text-xs text-muted-foreground">
            <input
              type="checkbox"
              checked={privsep}
              onChange={(e) => {
                setPrivsep(e.target.checked);
              }}
            />
            Privilege separation
          </label>
          <Button
            type="submit"
            size="sm"
            disabled={!tokenName.trim() || createToken.isPending}
          >
            {createToken.isPending ? "Creating..." : "Create Token"}
          </Button>
        </form>
      )}

      {error && (
        <ErrorBanner
          message={error}
          onDismiss={() => {
            setError("");
          }}
        />
      )}

      {/* The revoke and regenerate confirmations are held open while their
          request is in flight, for the reason the user delete's is (see
          AccessUsersSection). */}
      <AlertDialog
        open={revokeTarget !== null}
        onOpenChange={(open) => {
          if (!open && !deleteToken.isPending) setRevokeTarget(null);
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              Revoke {revokeTarget && fullTokenId(revokeTarget)}?
            </AlertDialogTitle>
            <AlertDialogDescription>
              Anything authenticating with this token loses access immediately.
              The secret cannot be recovered — a replacement has to be created
              and distributed.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={deleteToken.isPending}>
              Cancel
            </AlertDialogCancel>
            <AlertDialogAction
              className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
              disabled={deleteToken.isPending}
              onClick={(e: React.MouseEvent) => {
                e.preventDefault();
                if (revokeTarget) {
                  handleRevoke(revokeTarget, false);
                  holdFocusInDialog(e);
                }
              }}
            >
              {deleteToken.isPending ? "Revoking..." : "Revoke Token"}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <AlertDialog
        open={regenTarget !== null}
        onOpenChange={(open) => {
          if (!open && !updateToken.isPending) setRegenTarget(null);
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              Regenerate {regenTarget && fullTokenId(regenTarget)}?
            </AlertDialogTitle>
            <AlertDialogDescription>
              A new secret is issued and the current one stops working
              immediately. The new secret is shown once and cannot be retrieved
              afterwards.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={updateToken.isPending}>
              Cancel
            </AlertDialogCancel>
            <AlertDialogAction
              disabled={updateToken.isPending}
              onClick={(e: React.MouseEvent) => {
                e.preventDefault();
                if (regenTarget) {
                  handleRegenerate(regenTarget, false);
                  holdFocusInDialog(e);
                }
              }}
            >
              {updateToken.isPending ? "Regenerating..." : "Regenerate"}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      {minted && (
        <TokenSecretDialog
          fullTokenId={minted["full-tokenid"]}
          secret={minted.value}
          onClose={() => {
            setMinted(null);
          }}
        />
      )}

      {selfConflict && (
        <SelfCredentialConfirm
          // Likewise for another action or token: nothing typed carries over.
          key={`${selfConflict.action}:${selfConflict.tokenid}`}
          title={TOKEN_OVERRIDE[selfConflict.action].title}
          message={selfConflict.message}
          // The full "user@realm!name": the dialog body names the token that
          // way, so asking for the bare suffix would both mismatch what is on
          // screen and reduce the confirmation to a few characters.
          confirmValue={fullTokenId(selfConflict.tokenid)}
          actionLabel={TOKEN_OVERRIDE[selfConflict.action].actionLabel}
          // Held while either token request is out, not only the one this
          // override sends: the cautious hold, since either action can raise it.
          pending={deleteToken.isPending || updateToken.isPending}
          onCancel={() => {
            setSelfConflict(null);
          }}
          onConfirm={() => {
            if (selfConflict.action === "revoke")
              handleRevoke(selfConflict.tokenid, true);
            else handleRegenerate(selfConflict.tokenid, true);
          }}
        />
      )}
    </div>
  );
}

/**
 * Edits one Proxmox user, seeded from the per-user detail endpoint.
 *
 * Mounted only while a target is set, and keyed on it by the section, so the
 * form state resets for free on close, and on a change of account, rather than
 * needing an effect to clear it.
 *
 * The form is drawn only from an account that was actually read, and a save
 * carries only the fields the operator touched. Both follow from the update
 * being tristate per field — omitted leaves the stored value alone, empty
 * clears it — so sending what the form merely displays writes it back, and what
 * it displays can be wrong. Values cached before the Proxmox UI changed them
 * were written back over the change, re-enabling an account disabled there; and
 * the form of an account that failed to load showed every field empty with
 * "Account enabled" ticked, so saving it cleared the stored comment and e-mail
 * and enabled the account.
 *
 * Disabling an account is the interesting case: PVE checks the owning user when
 * it verifies an API token, so disabling the user Nexara authenticates as
 * breaks the cluster connection just as a delete would. The server refuses that
 * with a 409, and the type-to-confirm override is this dialog's own: it re-sends
 * the refused edit with force, and Cancel goes back to the form. It is not
 * handed up to the section, whose override is the DELETE's — an earlier version
 * did that, so confirming an edit force-deleted the user and all its tokens.
 */
function EditUserDialog({
  clusterId,
  userid,
  onClose,
}: {
  clusterId: string;
  userid: string;
  onClose: () => void;
}) {
  const userQuery = useAccessUser(clusterId, userid);
  const updateUser = useUpdateAccessUser(clusterId);

  // null means "untouched": the fetched value shows through until edited, and
  // the field stays out of the save.
  const [comment, setComment] = useState<string | null>(null);
  const [email, setEmail] = useState<string | null>(null);
  const [enable, setEnable] = useState<boolean | null>(null);
  const [error, setError] = useState("");
  // The edit as it was refused, so the override confirms exactly that one.
  const [conflict, setConflict] = useState<{
    message: string;
    edit: UpdateAccessUserInput;
  } | null>(null);

  const currentComment = comment ?? userQuery.data?.comment ?? "";
  const currentEmail = email ?? userQuery.data?.email ?? "";
  const currentEnable = enable ?? userQuery.data?.enable ?? true;

  // What the PUT carries: the touched fields and nothing else. Save is held
  // until there is at least one, so an edit is never empty.
  const changes = {
    ...(comment !== null ? { comment } : {}),
    ...(email !== null ? { email } : {}),
    ...(enable !== null ? { enable } : {}),
  };
  const touched = Object.keys(changes).length > 0;

  // The hook opts out of the global toast because the open dialog reports its
  // own errors, but mutate()'s per-call callbacks do not run once the component
  // is gone, so save settles through the promise instead, and a failure nobody
  // is looking at is toasted (see useSaveOutcome, which also keeps the toast
  // out of a session that has ended). This component IS the dialog, mounted
  // only while it is open.
  const settle = useSaveOutcome();

  const save = (edit: UpdateAccessUserInput, force: boolean) => {
    setError("");
    // Named: it can land on another page, or over another account's open Edit
    // dialog.
    const action = `Saving ${edit.userid}`;
    settle(updateUser.mutateAsync({ ...edit, force }), {
      action,
      // A forced save is past the self-credential refusal, so a 409 to it is
      // an error like any other, in the words the server gave it: only an
      // unforced one has an override to be sent to.
      lateFailure: (err) =>
        !force && isOverridable(err)
          ? refusedBeyondOverride(action)
          : undefined,
      // Not once dismissed: onClose would close whichever dialog has been
      // opened in this one's place.
      onSuccess: onClose,
      onError: (err) => {
        // A forced save is past the self-credential refusal, so whatever it
        // fails with is an error for the form, never a second override.
        if (!force && isOverridable(err)) {
          setConflict({ message: err.message, edit });
          return;
        }
        setConflict(null);
        setError(errorMessage(err, "Failed to update user"));
      },
    });
  };

  const handleSave = (e: React.SyntheticEvent) => {
    e.preventDefault();
    // Save is disabled until a field is touched, but a submit that does not go
    // through the button (form.requestSubmit) would still send an empty edit.
    if (!touched) return;
    save({ userid, ...changes }, false);
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
          <DialogTitle>Edit {userid}</DialogTitle>
        </DialogHeader>
        {/* Keyed on the account having been read rather than on isLoading: a
            read that is paused (offline, or retrying while the tab is in the
            background) is neither loading nor failed and has no data, and the
            form drawn for it would be the empty one. And on data rather than
            on isError, because TanStack keeps the last good data through a
            failed refresh, which the form reports in a notice instead of
            disappearing under the operator. */}
        {userQuery.data !== undefined ? (
          <form onSubmit={handleSave} className="space-y-4">
            {userQuery.isError && (
              <p role="status" className="text-xs text-destructive">
                {errorMessage(userQuery.error, "Failed to load user")} — showing
                the details as last read.
              </p>
            )}
            <div>
              <Label htmlFor="edit-comment">Comment</Label>
              <Input
                id="edit-comment"
                value={currentComment}
                onChange={(e) => {
                  setComment(e.target.value);
                }}
              />
            </div>
            <div>
              <Label htmlFor="edit-email">Email</Label>
              <Input
                id="edit-email"
                type="email"
                value={currentEmail}
                onChange={(e) => {
                  setEmail(e.target.value);
                }}
              />
            </div>
            <label className="flex items-center gap-2 text-sm">
              <input
                type="checkbox"
                checked={currentEnable}
                onChange={(e) => {
                  setEnable(e.target.checked);
                }}
              />
              Account enabled
            </label>
            {(userQuery.data.groups?.length ?? 0) > 0 && (
              <p className="text-xs text-muted-foreground">
                Groups: {userQuery.data.groups?.join(", ")} — edit these in the
                Proxmox UI.
              </p>
            )}
            {error && <p className="text-sm text-destructive">{error}</p>}
            <div className="flex justify-end gap-2">
              <Button type="button" variant="outline" onClick={onClose}>
                Cancel
              </Button>
              <Button type="submit" disabled={!touched || updateUser.isPending}>
                {updateUser.isPending ? "Saving..." : "Save"}
              </Button>
            </div>
          </form>
        ) : userQuery.isError ? (
          <div className="space-y-4">
            <p role="alert" className="text-sm text-destructive">
              {errorMessage(userQuery.error, "Failed to load user")}
            </p>
            <div className="flex justify-end">
              <Button
                type="button"
                variant="outline"
                onClick={() => {
                  void userQuery.refetch();
                }}
              >
                Retry
              </Button>
            </div>
          </div>
        ) : (
          <Skeleton className="h-32 w-full" />
        )}
        {/* Stacked over this dialog, which stays open beneath it, so
            dismissing it returns to the form with the edit intact. */}
        {conflict && (
          <SelfCredentialConfirm
            title="Saving this edit will cut off Nexara's access"
            message={conflict.message}
            confirmValue={conflict.edit.userid}
            actionLabel="Save Anyway"
            pending={updateUser.isPending}
            onCancel={() => {
              setConflict(null);
            }}
            onConfirm={() => {
              save(conflict.edit, true);
            }}
          />
        )}
      </DialogContent>
    </Dialog>
  );
}
