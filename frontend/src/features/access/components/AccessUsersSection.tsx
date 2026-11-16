import { Fragment, useState } from "react";
import {
  ChevronDown,
  ChevronRight,
  CircleAlert,
  Pencil,
  Plus,
  RefreshCw,
  Trash2,
} from "lucide-react";
import { toast } from "sonner";
import { Trans, useTranslation } from "react-i18next";
import { ClusterHistoryButton } from "@/features/history/components/ClusterHistoryButton";
import { clusterHistories } from "@/features/history/lib/cluster-histories";

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
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Panel } from "@/components/page/Panel";
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
import { isOverridable, refusedBeyondOverride } from "./selfCredentialRefusal";
import { useInCluster, useSingleFlight } from "./accessSaves";
import { holdFocusInDialog } from "./holdFocusInDialog";
import { DateTime } from "@/components/DateTime";

interface Props {
  clusterId: string;
  capabilities: AccessCapabilities;
}

/** Renders a PVE expiry timestamp; 0 or absent means "never". */
function expiryLabel(expire: number | undefined, never: string) {
  return (
    <DateTime
      value={expire === undefined ? null : expire * 1000}
      kind="date"
      fallback={never}
    />
  );
}

function errorMessage(err: unknown, fallback: string): string {
  return err instanceof ApiClientError ? err.message : fallback;
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
  const { t } = useTranslation("access");
  return (
    <div className="mb-3 flex items-start justify-between gap-2 rounded-[8px] bg-destructive/[0.08] px-3.5 py-2.5">
      <div className="flex min-w-0 items-start gap-2.5">
        <CircleAlert className="mt-0.5 h-4 w-4 shrink-0 text-destructive" />
        <p className="min-w-0 text-sm [overflow-wrap:anywhere]">{message}</p>
      </div>
      <Button
        variant="ghost"
        size="sm"
        className="h-7 shrink-0"
        onClick={onDismiss}
      >
        {t("users.dismiss")}
      </Button>
    </div>
  );
}

export function AccessUsersSection({ clusterId, capabilities }: Props) {
  const { t } = useTranslation("access");
  const { canManage } = useAuth();
  const usersQuery = useAccessUsers(clusterId);
  const createUser = useCreateAccessUser(clusterId);
  const deleteUser = useDeleteAccessUser(clusterId);
  const createUserOnce = useSingleFlight(createUser.mutateAsync);
  const deleteUserOnce = useSingleFlight(deleteUser.mutateAsync);
  const inCluster = useInCluster(clusterId);

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
    const sent = createUserOnce({
      userid: id,
      ...(comment ? { comment } : {}),
      ...(password ? { password } : {}),
    });
    if (sent === null) return;
    settleCreate(sent, {
      action: inCluster(t("users.creatingAction", { name: id })),
      // Not once the dialog has been dismissed: this would close, and clear,
      // whichever one has been opened in its place.
      onSuccess: () => {
        setCreateOpen(false);
        resetCreate();
      },
      onError: (err) => {
        setCreateError(errorMessage(err, t("users.createFailed")));
      },
    });
  };

  const handleDelete = (target: string, force: boolean) => {
    setSectionError("");
    const action = inCluster(t("users.deletingAction", { name: target }));
    const sent = deleteUserOnce({ userid: target, force });
    if (sent === null) return;
    settleDelete(sent, {
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
        setSectionError(errorMessage(err, t("users.deleteFailed")));
      },
    });
  };

  const users = usersQuery.data ?? [];
  const disabledUsers = users.filter((u) => u.enable === false).length;

  return (
    <Panel
      flush
      title={t("users.title")}
      aside={
        users.length === 0
          ? undefined
          : disabledUsers > 0
            ? t("users.asideDisabled", {
                count: users.length,
                disabled: disabledUsers,
              })
            : t("users.aside", { count: users.length })
      }
      actions={
        manageable ? (
          <Dialog
            open={createOpen}
            onOpenChange={(open) => {
              setCreateOpen(open);
              if (!open) resetCreate();
            }}
          >
            <DialogTrigger asChild>
              <Button size="sm">
                <Plus className="h-4 w-4" />
                {t("users.createUser")}
              </Button>
            </DialogTrigger>
            <DialogContent className="max-w-sm">
              <DialogHeader>
                <DialogTitle>{t("users.createTitle")}</DialogTitle>
                <DialogDescription>
                  {t("users.createDescription")}
                </DialogDescription>
              </DialogHeader>
              <form onSubmit={handleCreate} className="space-y-4">
                <div className="space-y-2">
                  <Label htmlFor="access-userid">{t("users.userId")}</Label>
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
                    <Trans
                      t={t}
                      i18nKey="users.userIdHint"
                      components={{ code: <code className="font-mono" /> }}
                    />
                  </p>
                </div>
                <div className="space-y-2">
                  <Label htmlFor="access-comment">{t("users.comment")}</Label>
                  <Input
                    id="access-comment"
                    value={comment}
                    onChange={(e) => {
                      setComment(e.target.value);
                    }}
                  />
                </div>
                <div className="space-y-2">
                  <Label htmlFor="access-password">{t("users.password")}</Label>
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
                    {t("users.passwordHint")}
                  </p>
                </div>
                {createError && (
                  <p className="text-sm text-destructive">{createError}</p>
                )}
                <DialogFooter>
                  <Button
                    type="submit"
                    disabled={!userid.trim() || createUser.isPending}
                  >
                    {createUser.isPending
                      ? t("common:creating")
                      : t("common:create")}
                  </Button>
                </DialogFooter>
              </form>
            </DialogContent>
          </Dialog>
        ) : undefined
      }
    >
      <>
        {sectionError && (
          <div className="px-[18px]">
            <ErrorBanner
              message={sectionError}
              onDismiss={() => {
                setSectionError("");
              }}
            />
          </div>
        )}

        {usersQuery.isLoading ? (
          <div className="px-[18px] pb-1">
            <Skeleton className="h-24 w-full" />
          </div>
        ) : !usersQuery.data || usersQuery.data.length === 0 ? (
          <p className="px-[18px] pb-1 text-sm text-muted-foreground">
            {t("users.none")}
          </p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="w-8" />
                <TableHead>{t("users.userId")}</TableHead>
                <TableHead>{t("users.state")}</TableHead>
                <TableHead>{t("users.groups")}</TableHead>
                <TableHead>{t("users.comment")}</TableHead>
                {manageable && (
                  <TableHead className="w-0">
                    <span className="sr-only">{t("common:actions")}</span>
                  </TableHead>
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
                      <TableCell className="text-muted-foreground">
                        {isOpen ? (
                          <ChevronDown className="h-4 w-4" />
                        ) : (
                          <ChevronRight className="h-4 w-4" />
                        )}
                      </TableCell>
                      <TableCell className="font-mono text-[12.5px] font-medium">
                        {user.userid}
                        <ClusterHistoryButton
                          history={clusterHistories.accessUser(
                            clusterId,
                            user.userid,
                          )}
                          title={user.userid}
                        />
                      </TableCell>
                      <TableCell>
                        <Badge
                          variant={
                            user.enable === false ? "secondary" : "healthy"
                          }
                        >
                          {user.enable === false
                            ? t("common:disabled")
                            : t("common:enabled")}
                        </Badge>
                      </TableCell>
                      <TableCell className="text-muted-foreground">
                        {user.groups || "—"}
                      </TableCell>
                      <TableCell className="text-muted-foreground">
                        {user.comment || "—"}
                      </TableCell>
                      {manageable && (
                        <TableCell className="whitespace-nowrap py-1 text-right">
                          <Button
                            variant="ghost"
                            size="icon"
                            className="h-8 w-8 text-muted-foreground"
                            aria-label={t("editLabel", { name: user.userid })}
                            onClick={(e) => {
                              e.stopPropagation();
                              setEditTarget(user.userid);
                            }}
                          >
                            <Pencil className="h-4 w-4" />
                          </Button>
                          <Button
                            variant="ghost"
                            size="icon"
                            className="h-8 w-8 text-muted-foreground hover:text-destructive"
                            aria-label={t("deleteLabel", { name: user.userid })}
                            onClick={(e) => {
                              e.stopPropagation();
                              setDeleteTarget(user);
                            }}
                          >
                            <Trash2 className="h-4 w-4" />
                          </Button>
                        </TableCell>
                      )}
                    </TableRow>
                    {isOpen && (
                      <TableRow className="hover:bg-transparent">
                        <TableCell
                          colSpan={columns}
                          className="bg-foreground/[0.03] py-3 pl-[3.25rem]"
                        >
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
                {t("users.deleteTitle", { name: deleteTarget?.userid ?? "" })}
              </AlertDialogTitle>
              <AlertDialogDescription>
                {t("users.deleteDescription")}
              </AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel disabled={deleteUser.isPending}>
                {t("common:cancel")}
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
                {deleteUser.isPending
                  ? t("common:deleting")
                  : t("users.deleteUser")}
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
            title={t("users.deleteConflictTitle")}
            message={deleteConflict.message}
            confirmValue={deleteConflict.userid}
            actionLabel={t("users.deleteUser")}
            pending={deleteUser.isPending}
            onCancel={() => {
              setDeleteConflict(null);
            }}
            onConfirm={() => {
              handleDelete(deleteConflict.userid, true);
            }}
          />
        )}
      </>
    </Panel>
  );
}

/**
 * What each token override says. Its title names the action it confirms and its
 * button is that action's verb; kept as pairs so a title and its button cannot
 * be crossed. Which request the button sends is chosen in onConfirm.
 */
const TOKEN_OVERRIDE = {
  revoke: {
    title: "tokens.revokeConflictTitle",
    actionLabel: "tokens.revokeToken",
  },
  regenerate: {
    title: "tokens.regenerateConflictTitle",
    actionLabel: "tokens.regenerateToken",
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
  const { t } = useTranslation("access");
  const tokensQuery = useAccessTokens(clusterId, userid);
  const createToken = useCreateAccessToken(clusterId);
  const updateToken = useUpdateAccessToken(clusterId);
  const deleteToken = useDeleteAccessToken(clusterId);
  const createTokenOnce = useSingleFlight(createToken.mutateAsync);
  const updateTokenOnce = useSingleFlight(updateToken.mutateAsync);
  const deleteTokenOnce = useSingleFlight(deleteToken.mutateAsync);
  const inCluster = useInCluster(clusterId);

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
    const token = inCluster(fullTokenId(tokenid));
    toast.error(
      regenerated
        ? t("tokens.regeneratedSecretNotShown", { token })
        : t("tokens.createdSecretNotShown", { token }),
      { duration: SECRET_NOTICE_MS },
    );
  };

  const handleCreate = (e: React.SyntheticEvent) => {
    e.preventDefault();
    setError("");
    const name = tokenName.trim();
    const sent = createTokenOnce({
      userid,
      tokenid: name,
      privsep,
      ...(tokenComment ? { comment: tokenComment } : {}),
    });
    if (sent === null) return;
    settle(sent, {
      action: inCluster(
        t("tokens.creatingAction", { token: fullTokenId(name) }),
      ),
      onSuccess: (created) => {
        setMinted(created);
        setTokenName("");
        setTokenComment("");
        setPrivsep(true);
      },
      onError: (err) => {
        setError(errorMessage(err, t("tokens.createFailed")));
      },
      onLateSuccess: () => {
        secretNotShown(name, false);
      },
    });
  };

  const handleRevoke = (tokenid: string, force: boolean) => {
    setError("");
    const action = inCluster(
      t("tokens.revokingAction", { token: fullTokenId(tokenid) }),
    );
    const sent = deleteTokenOnce({ userid, tokenid, force });
    if (sent === null) return;
    settle(sent, {
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
        setError(errorMessage(err, t("tokens.revokeFailed")));
      },
    });
  };

  const handleRegenerate = (tokenid: string, force: boolean) => {
    setError("");
    const action = inCluster(
      t("tokens.regeneratingAction", { token: fullTokenId(tokenid) }),
    );
    const sent = updateTokenOnce({ userid, tokenid, regenerate: true, force });
    if (sent === null) return;
    settle(sent, {
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
        setError(errorMessage(err, t("tokens.regenerateFailed")));
      },
      // The old secret stopped working when the new one was issued.
      onLateSuccess: () => {
        secretNotShown(tokenid, true);
      },
    });
  };

  return (
    <div className="space-y-3">
      <div className="text-sm font-semibold">{t("tokens.title")}</div>

      {tokensQuery.isLoading ? (
        <Skeleton className="h-12 w-full" />
      ) : !tokensQuery.data || tokensQuery.data.length === 0 ? (
        <p className="text-sm text-muted-foreground">{t("tokens.none")}</p>
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="pl-0">{t("tokens.token")}</TableHead>
              <TableHead>{t("tokens.privileges")}</TableHead>
              <TableHead>{t("tokens.expires")}</TableHead>
              <TableHead>{t("users.comment")}</TableHead>
              {manageable && (
                <TableHead className="w-0 pr-0">
                  <span className="sr-only">{t("common:actions")}</span>
                </TableHead>
              )}
            </TableRow>
          </TableHeader>
          <TableBody>
            {tokensQuery.data.map((token) => (
              <TableRow key={token.tokenid}>
                <TableCell className="pl-0 font-mono text-[12.5px]">
                  {fullTokenId(token.tokenid)}
                </TableCell>
                <TableCell>
                  {/* A token that carries all of its user's privileges is the
                      one worth a second look; a separated one is the norm. */}
                  {token.privsep ? (
                    t("tokens.separated")
                  ) : (
                    <Badge variant="warning">
                      {t("tokens.fullUserPrivileges")}
                    </Badge>
                  )}
                </TableCell>
                <TableCell className="text-muted-foreground">
                  {expiryLabel(token.expire, t("tokens.never"))}
                </TableCell>
                <TableCell className="text-muted-foreground">
                  {token.comment || "—"}
                </TableCell>
                {manageable && (
                  <TableCell className="whitespace-nowrap py-1 pr-0 text-right">
                    <Button
                      variant="ghost"
                      size="icon"
                      className="h-8 w-8 text-muted-foreground"
                      aria-label={t("tokens.regenerateLabel", {
                        name: token.tokenid,
                      })}
                      onClick={() => {
                        setRegenTarget(token.tokenid);
                      }}
                    >
                      <RefreshCw className="h-4 w-4" />
                    </Button>
                    <Button
                      variant="ghost"
                      size="icon"
                      className="h-8 w-8 text-muted-foreground hover:text-destructive"
                      aria-label={t("tokens.revokeLabel", {
                        name: token.tokenid,
                      })}
                      onClick={() => {
                        setRevokeTarget(token.tokenid);
                      }}
                    >
                      <Trash2 className="h-4 w-4" />
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
              {t("tokens.newTokenName")}
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
              {t("users.comment")}
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
            {t("tokens.privilegeSeparation")}
          </label>
          <Button
            type="submit"
            size="sm"
            variant="outline"
            disabled={!tokenName.trim() || createToken.isPending}
          >
            {createToken.isPending
              ? t("common:creating")
              : t("tokens.createToken")}
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
              {t("tokens.revokeTitle", {
                token: revokeTarget ? fullTokenId(revokeTarget) : "",
              })}
            </AlertDialogTitle>
            <AlertDialogDescription>
              {t("tokens.revokeDescription")}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={deleteToken.isPending}>
              {t("common:cancel")}
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
              {deleteToken.isPending
                ? t("tokens.revoking")
                : t("tokens.revokeToken")}
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
              {t("tokens.regenerateTitle", {
                token: regenTarget ? fullTokenId(regenTarget) : "",
              })}
            </AlertDialogTitle>
            <AlertDialogDescription>
              {t("tokens.regenerateDescription")}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={updateToken.isPending}>
              {t("common:cancel")}
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
              {updateToken.isPending
                ? t("tokens.regenerating")
                : t("tokens.regenerate")}
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
          title={t(TOKEN_OVERRIDE[selfConflict.action].title)}
          message={selfConflict.message}
          // The full "user@realm!name": the dialog body names the token that
          // way, so asking for the bare suffix would both mismatch what is on
          // screen and reduce the confirmation to a few characters.
          confirmValue={fullTokenId(selfConflict.tokenid)}
          actionLabel={t(TOKEN_OVERRIDE[selfConflict.action].actionLabel)}
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
  const { t } = useTranslation("access");
  const userQuery = useAccessUser(clusterId, userid);
  const updateUser = useUpdateAccessUser(clusterId);
  const updateUserOnce = useSingleFlight(updateUser.mutateAsync);
  const inCluster = useInCluster(clusterId);

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
    const action = inCluster(t("users.savingAction", { name: edit.userid }));
    const sent = updateUserOnce({ ...edit, force });
    if (sent === null) return;
    settle(sent, {
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
        setError(errorMessage(err, t("users.updateFailed")));
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
          <DialogTitle>{t("editTitle", { name: userid })}</DialogTitle>
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
                {t("users.loadFailedStale", {
                  error: errorMessage(userQuery.error, t("users.loadFailed")),
                })}
              </p>
            )}
            <div className="space-y-2">
              <Label htmlFor="edit-comment">{t("users.comment")}</Label>
              <Input
                id="edit-comment"
                value={currentComment}
                onChange={(e) => {
                  setComment(e.target.value);
                }}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="edit-email">{t("users.email")}</Label>
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
              {t("users.accountEnabled")}
            </label>
            {(userQuery.data.groups?.length ?? 0) > 0 && (
              <p className="text-xs text-muted-foreground">
                {t("users.groupsHint", {
                  groups: userQuery.data.groups?.join(", "),
                })}
              </p>
            )}
            {error && <p className="text-sm text-destructive">{error}</p>}
            <DialogFooter>
              <Button type="button" variant="outline" onClick={onClose}>
                {t("common:cancel")}
              </Button>
              <Button type="submit" disabled={!touched || updateUser.isPending}>
                {updateUser.isPending ? t("common:saving") : t("common:save")}
              </Button>
            </DialogFooter>
          </form>
        ) : userQuery.isError ? (
          <div className="space-y-4">
            <p role="alert" className="text-sm text-destructive">
              {errorMessage(userQuery.error, t("users.loadFailed"))}
            </p>
            <div className="flex justify-end">
              <Button
                type="button"
                variant="outline"
                onClick={() => {
                  void userQuery.refetch();
                }}
              >
                {t("users.retry")}
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
            title={t("users.saveConflictTitle")}
            message={conflict.message}
            confirmValue={conflict.edit.userid}
            actionLabel={t("users.saveAnyway")}
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
