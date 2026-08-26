import { Fragment, useState } from "react";
import { AlertTriangle, ChevronDown, ChevronRight, KeyRound, Pencil, Plus, RefreshCw, Trash2 } from "lucide-react";

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

import {
  type AccessCapabilities,
  type AccessTokenCreated,
  type AccessUser,
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
 * A failure banner rendered in the section body rather than inside a dialog.
 *
 * These mutations opt out of the global error toast, so wherever their errors
 * are shown has to be visible at the moment they happen. An earlier version
 * wrote delete failures into the create-user dialog's error slot, which meant a
 * failed delete showed nothing at all — and then surfaced, misattributed, the
 * next time the operator opened the create form.
 */
function ErrorBanner({ message, onDismiss }: { message: string; onDismiss: () => void }) {
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
  // Set when the server refuses because the target is Nexara's own credential.
  const [selfConflict, setSelfConflict] = useState<{ message: string; userid: string } | null>(null);

  const manageable = canManage("access") && capabilities.canModifyUsers;
  const columns = manageable ? 6 : 5;

  const resetCreate = () => {
    setUserid("");
    setComment("");
    setPassword("");
    setCreateError("");
  };

  const handleCreate = (e: React.SyntheticEvent) => {
    e.preventDefault();
    setCreateError("");
    createUser.mutate(
      {
        userid: userid.trim(),
        ...(comment ? { comment } : {}),
        ...(password ? { password } : {}),
      },
      {
        onSuccess: () => {
          setCreateOpen(false);
          resetCreate();
        },
        onError: (err) => { setCreateError(errorMessage(err, "Failed to create user")); },
      },
    );
  };

  const handleDelete = (target: string, force: boolean) => {
    setSectionError("");
    deleteUser.mutate(
      { userid: target, force },
      {
        onSuccess: () => {
          setSelfConflict(null);
          setDeleteTarget(null);
        },
        onError: (err) => {
          setDeleteTarget(null);
          // 409 means the target is the credential Nexara authenticates with.
          // Hand it to the type-to-confirm override rather than reporting a
          // plain failure — it is a refusal, not an error.
          if (err instanceof ApiClientError && err.status === 409) {
            setSelfConflict({ message: err.message, userid: target });
            return;
          }
          setSelfConflict(null);
          setSectionError(errorMessage(err, "Failed to delete user"));
        },
      },
    );
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
                    onChange={(e) => { setUserid(e.target.value); }}
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
                    onChange={(e) => { setComment(e.target.value); }}
                  />
                </div>
                <div>
                  <Label htmlFor="access-password">Password</Label>
                  <Input
                    id="access-password"
                    type="password"
                    value={password}
                    onChange={(e) => { setPassword(e.target.value); }}
                    autoComplete="new-password"
                  />
                  <p className="mt-1 text-xs text-muted-foreground">
                    Leave empty for a token-only account that cannot log in interactively.
                  </p>
                </div>
                {createError && <p className="text-sm text-destructive">{createError}</p>}
                <Button type="submit" disabled={!userid.trim() || createUser.isPending}>
                  {createUser.isPending ? "Creating..." : "Create"}
                </Button>
              </form>
            </DialogContent>
          </Dialog>
        )}
      </CardHeader>

      <CardContent>
        {sectionError && (
          <ErrorBanner message={sectionError} onDismiss={() => { setSectionError(""); }} />
        )}

        {usersQuery.isLoading ? (
          <Skeleton className="h-24 w-full" />
        ) : !usersQuery.data || usersQuery.data.length === 0 ? (
          <p className="text-sm text-muted-foreground">No Proxmox users found.</p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="w-8" />
                <TableHead>User ID</TableHead>
                <TableHead>Status</TableHead>
                <TableHead>Groups</TableHead>
                <TableHead>Comment</TableHead>
                {manageable && <TableHead className="text-right">Actions</TableHead>}
              </TableRow>
            </TableHeader>
            <TableBody>
              {usersQuery.data.map((user: AccessUser) => {
                const isOpen = expanded === user.userid;
                return (
                  <Fragment key={user.userid}>
                    <TableRow
                      className="cursor-pointer"
                      onClick={() => { setExpanded(isOpen ? null : user.userid); }}
                    >
                      <TableCell>
                        {isOpen ? <ChevronDown className="h-4 w-4" /> : <ChevronRight className="h-4 w-4" />}
                      </TableCell>
                      <TableCell className="font-mono text-xs">{user.userid}</TableCell>
                      <TableCell>
                        <Badge variant={user.enable === false ? "secondary" : "default"}>
                          {user.enable === false ? "Disabled" : "Enabled"}
                        </Badge>
                      </TableCell>
                      <TableCell className="text-muted-foreground">{user.groups || "—"}</TableCell>
                      <TableCell className="text-muted-foreground">{user.comment || "—"}</TableCell>
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
            less destructive group and role deletes already do. */}
        <AlertDialog
          open={deleteTarget !== null}
          onOpenChange={(open) => { if (!open) setDeleteTarget(null); }}
        >
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle>Delete {deleteTarget?.userid}?</AlertDialogTitle>
              <AlertDialogDescription>
                This removes the Proxmox user and every API token it owns. Anything
                authenticating with one of those tokens loses access immediately, and the
                secrets cannot be recovered. This cannot be undone.
              </AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel>Cancel</AlertDialogCancel>
              <AlertDialogAction
                className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
                disabled={deleteUser.isPending}
                onClick={(e: React.MouseEvent) => {
                  e.preventDefault();
                  if (deleteTarget) handleDelete(deleteTarget.userid, false);
                }}
              >
                {deleteUser.isPending ? "Deleting..." : "Delete User"}
              </AlertDialogAction>
            </AlertDialogFooter>
          </AlertDialogContent>
        </AlertDialog>

        {editTarget !== null && (
          <EditUserDialog
            clusterId={clusterId}
            userid={editTarget}
            onClose={() => { setEditTarget(null); }}
            onSelfConflict={(message, uid) => {
              setEditTarget(null);
              setSelfConflict({ message, userid: uid });
            }}
          />
        )}

        {selfConflict && (
          <SelfCredentialConfirm
            message={selfConflict.message}
            confirmValue={selfConflict.userid}
            actionLabel="Delete User"
            pending={deleteUser.isPending}
            onCancel={() => { setSelfConflict(null); }}
            onConfirm={() => { handleDelete(selfConflict.userid, true); }}
          />
        )}
      </CardContent>
    </Card>
  );
}

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
  const [selfConflict, setSelfConflict] = useState<
    { message: string; tokenid: string; action: "revoke" | "regenerate" } | null
  >(null);

  const fullTokenId = (tokenid: string) => `${userid}!${tokenid}`;

  const handleCreate = (e: React.SyntheticEvent) => {
    e.preventDefault();
    setError("");
    createToken.mutate(
      {
        userid,
        tokenid: tokenName.trim(),
        privsep,
        ...(tokenComment ? { comment: tokenComment } : {}),
      },
      {
        onSuccess: (created) => {
          setMinted(created);
          setTokenName("");
          setTokenComment("");
          setPrivsep(true);
        },
        onError: (err) => { setError(errorMessage(err, "Failed to create token")); },
      },
    );
  };

  const handleRevoke = (tokenid: string, force: boolean) => {
    setError("");
    deleteToken.mutate(
      { userid, tokenid, force },
      {
        onSuccess: () => {
          setSelfConflict(null);
          setRevokeTarget(null);
        },
        onError: (err) => {
          setRevokeTarget(null);
          if (err instanceof ApiClientError && err.status === 409) {
            setSelfConflict({ message: err.message, tokenid, action: "revoke" });
            return;
          }
          setSelfConflict(null);
          setError(errorMessage(err, "Failed to revoke token"));
        },
      },
    );
  };

  const handleRegenerate = (tokenid: string, force: boolean) => {
    setError("");
    updateToken.mutate(
      { userid, tokenid, regenerate: true, force },
      {
        onSuccess: (updated) => {
          setSelfConflict(null);
          setRegenTarget(null);
          setMinted(updated);
        },
        onError: (err) => {
          setRegenTarget(null);
          if (err instanceof ApiClientError && err.status === 409) {
            setSelfConflict({ message: err.message, tokenid, action: "regenerate" });
            return;
          }
          setSelfConflict(null);
          setError(errorMessage(err, "Failed to regenerate token"));
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
        <p className="text-sm text-muted-foreground">No API tokens for this user.</p>
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Token</TableHead>
              <TableHead>Privilege Separation</TableHead>
              <TableHead>Expires</TableHead>
              <TableHead>Comment</TableHead>
              {manageable && <TableHead className="text-right">Actions</TableHead>}
            </TableRow>
          </TableHeader>
          <TableBody>
            {tokensQuery.data.map((token) => (
              <TableRow key={token.tokenid}>
                <TableCell className="font-mono text-xs">{fullTokenId(token.tokenid)}</TableCell>
                <TableCell>
                  <Badge variant={token.privsep ? "default" : "destructive"}>
                    {token.privsep ? "Separated" : "Full user privileges"}
                  </Badge>
                </TableCell>
                <TableCell className="text-muted-foreground">{expiryLabel(token.expire)}</TableCell>
                <TableCell className="text-muted-foreground">{token.comment || "—"}</TableCell>
                {manageable && (
                  <TableCell className="text-right">
                    <Button
                      variant="ghost"
                      size="sm"
                      aria-label={`Regenerate ${token.tokenid}`}
                      onClick={() => { setRegenTarget(token.tokenid); }}
                    >
                      <RefreshCw className="h-4 w-4" />
                    </Button>
                    <Button
                      variant="ghost"
                      size="sm"
                      aria-label={`Revoke ${token.tokenid}`}
                      onClick={() => { setRevokeTarget(token.tokenid); }}
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
        <form onSubmit={handleCreate} className="flex flex-wrap items-end gap-2 pt-1">
          <div className="min-w-40">
            <Label htmlFor={`token-name-${userid}`} className="text-xs">
              New token name
            </Label>
            <Input
              id={`token-name-${userid}`}
              value={tokenName}
              onChange={(e) => { setTokenName(e.target.value); }}
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
              onChange={(e) => { setTokenComment(e.target.value); }}
              className="h-8"
            />
          </div>
          <label className="flex items-center gap-2 pb-1 text-xs text-muted-foreground">
            <input
              type="checkbox"
              checked={privsep}
              onChange={(e) => { setPrivsep(e.target.checked); }}
            />
            Privilege separation
          </label>
          <Button type="submit" size="sm" disabled={!tokenName.trim() || createToken.isPending}>
            {createToken.isPending ? "Creating..." : "Create Token"}
          </Button>
        </form>
      )}

      {error && <ErrorBanner message={error} onDismiss={() => { setError(""); }} />}

      <AlertDialog
        open={revokeTarget !== null}
        onOpenChange={(open) => { if (!open) setRevokeTarget(null); }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Revoke {revokeTarget && fullTokenId(revokeTarget)}?</AlertDialogTitle>
            <AlertDialogDescription>
              Anything authenticating with this token loses access immediately. The secret
              cannot be recovered — a replacement has to be created and distributed.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
              disabled={deleteToken.isPending}
              onClick={(e: React.MouseEvent) => {
                e.preventDefault();
                if (revokeTarget) handleRevoke(revokeTarget, false);
              }}
            >
              {deleteToken.isPending ? "Revoking..." : "Revoke Token"}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <AlertDialog
        open={regenTarget !== null}
        onOpenChange={(open) => { if (!open) setRegenTarget(null); }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Regenerate {regenTarget && fullTokenId(regenTarget)}?</AlertDialogTitle>
            <AlertDialogDescription>
              A new secret is issued and the current one stops working immediately. The new
              secret is shown once and cannot be retrieved afterwards.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              disabled={updateToken.isPending}
              onClick={(e: React.MouseEvent) => {
                e.preventDefault();
                if (regenTarget) handleRegenerate(regenTarget, false);
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
          onClose={() => { setMinted(null); }}
        />
      )}

      {selfConflict && (
        <SelfCredentialConfirm
          message={selfConflict.message}
          // The full "user@realm!name": the dialog body names the token that
          // way, so asking for the bare suffix would both mismatch what is on
          // screen and reduce the confirmation to a few characters.
          confirmValue={fullTokenId(selfConflict.tokenid)}
          actionLabel={selfConflict.action === "revoke" ? "Revoke Token" : "Regenerate Token"}
          pending={deleteToken.isPending || updateToken.isPending}
          onCancel={() => { setSelfConflict(null); }}
          onConfirm={() => {
            if (selfConflict.action === "revoke") handleRevoke(selfConflict.tokenid, true);
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
 * Mounted only while a target is set, so the form state resets for free on
 * close rather than needing an effect to clear it.
 *
 * Disabling an account is the interesting case: PVE checks the owning user when
 * it verifies an API token, so disabling the user Nexara authenticates as
 * breaks the cluster connection just as a delete would. The server answers that
 * with the same 409 the delete path uses, which is handed back up to the
 * type-to-confirm override.
 */
function EditUserDialog({
  clusterId,
  userid,
  onClose,
  onSelfConflict,
}: {
  clusterId: string;
  userid: string;
  onClose: () => void;
  onSelfConflict: (message: string, userid: string) => void;
}) {
  const userQuery = useAccessUser(clusterId, userid);
  const updateUser = useUpdateAccessUser(clusterId);

  // null means "untouched", so the fetched value shows through until edited.
  const [comment, setComment] = useState<string | null>(null);
  const [email, setEmail] = useState<string | null>(null);
  const [enable, setEnable] = useState<boolean | null>(null);
  const [error, setError] = useState("");

  const currentComment = comment ?? userQuery.data?.comment ?? "";
  const currentEmail = email ?? userQuery.data?.email ?? "";
  const currentEnable = enable ?? userQuery.data?.enable ?? true;

  const handleSave = (e: React.SyntheticEvent) => {
    e.preventDefault();
    setError("");
    updateUser.mutate(
      { userid, comment: currentComment, email: currentEmail, enable: currentEnable },
      {
        onSuccess: onClose,
        onError: (err) => {
          if (err instanceof ApiClientError && err.status === 409) {
            onSelfConflict(err.message, userid);
            return;
          }
          setError(errorMessage(err, "Failed to update user"));
        },
      },
    );
  };

  return (
    <Dialog open onOpenChange={(open) => { if (!open) onClose(); }}>
      <DialogContent className="max-w-sm">
        <DialogHeader>
          <DialogTitle>Edit {userid}</DialogTitle>
        </DialogHeader>
        {userQuery.isLoading ? (
          <Skeleton className="h-32 w-full" />
        ) : (
          <form onSubmit={handleSave} className="space-y-4">
            <div>
              <Label htmlFor="edit-comment">Comment</Label>
              <Input
                id="edit-comment"
                value={currentComment}
                onChange={(e) => { setComment(e.target.value); }}
              />
            </div>
            <div>
              <Label htmlFor="edit-email">Email</Label>
              <Input
                id="edit-email"
                type="email"
                value={currentEmail}
                onChange={(e) => { setEmail(e.target.value); }}
              />
            </div>
            <label className="flex items-center gap-2 text-sm">
              <input
                type="checkbox"
                checked={currentEnable}
                onChange={(e) => { setEnable(e.target.checked); }}
              />
              Account enabled
            </label>
            {(userQuery.data?.groups?.length ?? 0) > 0 && (
              <p className="text-xs text-muted-foreground">
                Groups: {userQuery.data?.groups?.join(", ")} — edit these in the Proxmox UI.
              </p>
            )}
            {error && <p className="text-sm text-destructive">{error}</p>}
            <div className="flex justify-end gap-2">
              <Button type="button" variant="outline" onClick={onClose}>
                Cancel
              </Button>
              <Button type="submit" disabled={updateUser.isPending}>
                {updateUser.isPending ? "Saving..." : "Save"}
              </Button>
            </div>
          </form>
        )}
      </DialogContent>
    </Dialog>
  );
}
