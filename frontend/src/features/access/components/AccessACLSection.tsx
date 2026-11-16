import { useState } from "react";
import { Plus, Trash2 } from "lucide-react";
import { Trans, useTranslation } from "react-i18next";

import { Button } from "@/components/ui/button";
import { Panel } from "@/components/page/Panel";
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
  DialogFooter,
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
  type UpdateACLInput,
  type AccessACLEntry,
  type AccessCapabilities,
  useAccessACL,
  useAccessGroups,
  useAccessRoles,
  useAccessUsers,
  useUpdateAccessACL,
} from "../api/access-queries";
import { SelfCredentialConfirm } from "./SelfCredentialConfirm";
import { isOverridable, refusedBeyondOverride } from "./selfCredentialRefusal";
import { useInCluster, useSingleFlight } from "./accessSaves";

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
  const { t } = useTranslation("access");
  const { canManage } = useAuth();
  const aclQuery = useAccessACL(clusterId);
  const rolesQuery = useAccessRoles(clusterId);
  const usersQuery = useAccessUsers(clusterId);
  const groupsQuery = useAccessGroups(clusterId);
  const updateACL = useUpdateAccessACL(clusterId);
  // Grant and revoke share the mutation, but not the guard: each is its own save.
  const grantOnce = useSingleFlight(updateACL.mutateAsync);
  const revokeOnce = useSingleFlight(updateACL.mutateAsync);
  const inCluster = useInCluster(clusterId);

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
  // The server's refusal of a grant or revoke for Nexara's own token, its user
  // or one of the user's groups, and what its override re-sends with force. The
  // grant's is stacked over the grant dialog, so Cancel goes back to the form.
  const [grantConflict, setGrantConflict] = useState<{
    message: string;
    input: UpdateACLInput;
    subject: string;
  } | null>(null);
  const [revokeConflict, setRevokeConflict] = useState<{
    message: string;
    entry: AccessACLEntry;
  } | null>(null);

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
    revokeTarget !== null
      ? `ask:${entryKey(revokeTarget)}`
      : revokeConflict
        ? `force:${entryKey(revokeConflict.entry)}`
        : null,
  );

  const subjectField = (kind: SubjectKind): "users" | "groups" | "tokens" =>
    kind === "user" ? "users" : kind === "group" ? "groups" : "tokens";

  const handleGrant = (e: React.SyntheticEvent) => {
    e.preventDefault();
    const onPath = path.trim();
    const to = subject.trim();
    sendGrant(
      {
        path: onPath,
        roles: role,
        [subjectField(subjectKind)]: to,
        propagate,
      },
      to,
      false,
    );
  };

  const sendGrant = (input: UpdateACLInput, to: string, force: boolean) => {
    setError("");
    const action = inCluster(
      t("acl.grantingAction", {
        role: input.roles,
        path: input.path,
        subject: to,
      }),
    );
    const sent = grantOnce({ ...input, force });
    if (sent === null) return;
    settleGrant(sent, {
      action,
      lateFailure: (err) =>
        !force && isOverridable(err)
          ? refusedBeyondOverride(action)
          : undefined,
      // Not once the dialog has been dismissed: this would close, and clear,
      // whichever one has been opened in its place.
      onSuccess: () => {
        setGrantConflict(null);
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
        setGrantConflict(null);
        // A forced grant is past the refusal: its 409 is an error like any other.
        if (!force && isOverridable(err)) {
          setGrantConflict({ message: err.message, input, subject: to });
          return;
        }
        setError(
          err instanceof ApiClientError ? err.message : t("acl.grantFailed"),
        );
      },
    });
  };

  const handleRevoke = (entry: AccessACLEntry, force: boolean) => {
    setRevokeError("");
    const action = inCluster(
      t("acl.revokingAction", {
        role: entry.roleid,
        path: entry.path,
        subject: entry.ugid,
      }),
    );
    const kind =
      entry.type === "group"
        ? "groups"
        : entry.type === "token"
          ? "tokens"
          : "users";
    const sent = revokeOnce({
      path: entry.path,
      roles: entry.roleid,
      [kind]: entry.ugid,
      delete: true,
      force,
    });
    if (sent === null) return;
    settleRevoke(sent, {
      action,
      lateFailure: (err) =>
        !force && isOverridable(err)
          ? refusedBeyondOverride(action)
          : undefined,
      // These run only while the confirmation that sent the request is still
      // the one open — this entry's, not another's that has replaced it — so
      // closing it cannot close another's: useSaveOutcome holds them to that,
      // and nothing here checks it a second time.
      onSuccess: () => {
        setRevokeTarget(null);
        setRevokeConflict(null);
      },
      onError: (err) => {
        setRevokeConflict(null);
        if (!force && isOverridable(err)) {
          setRevokeTarget(null);
          setRevokeConflict({ message: err.message, entry });
          return;
        }
        setRevokeError(
          err instanceof ApiClientError ? err.message : t("acl.revokeFailed"),
        );
        setRevokeTarget(null);
      },
    });
  };

  const entryCount = aclQuery.data?.length ?? 0;

  return (
    <Panel
      flush
      title={t("acl.title")}
      aside={
        entryCount === 0 ? undefined : t("acl.aside", { count: entryCount })
      }
      actions={
        manageable ? (
          <Dialog
            open={grantOpen}
            onOpenChange={(open) => {
              setGrantOpen(open);
              if (!open) setGrantConflict(null);
              // Clear on close: error was only reset at the start of the next
              // submit, so reopening the dialog showed the previous failure.
              if (!open) setError("");
            }}
          >
            <DialogTrigger asChild>
              <Button size="sm">
                <Plus className="h-4 w-4" />
                {t("acl.grantAccess")}
              </Button>
            </DialogTrigger>
            <DialogContent className="max-w-md">
              <DialogHeader>
                <DialogTitle>{t("acl.grantAccess")}</DialogTitle>
                <DialogDescription>
                  {t("acl.grantDescription")}
                </DialogDescription>
              </DialogHeader>
              <form onSubmit={handleGrant} className="space-y-4">
                <div className="space-y-2">
                  <Label htmlFor="acl-path">{t("acl.path")}</Label>
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
                    <Trans
                      t={t}
                      i18nKey="acl.pathHint"
                      components={{ code: <code className="font-mono" /> }}
                    />
                  </p>
                </div>

                <div className="space-y-2">
                  <Label htmlFor="acl-role">{t("acl.role")}</Label>
                  <Select value={role} onValueChange={setRole}>
                    <SelectTrigger id="acl-role">
                      <SelectValue placeholder={t("acl.selectRole")} />
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

                <div className="space-y-2">
                  <Label htmlFor="acl-kind">{t("acl.subjectType")}</Label>
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
                      <SelectItem value="user">{t("acl.kindUser")}</SelectItem>
                      <SelectItem value="group">
                        {t("acl.kindGroup")}
                      </SelectItem>
                      <SelectItem value="token">
                        {t("acl.kindToken")}
                      </SelectItem>
                    </SelectContent>
                  </Select>
                </div>

                <div className="space-y-2">
                  <Label htmlFor="acl-subject">
                    {subjectKind === "user"
                      ? t("acl.user")
                      : subjectKind === "group"
                        ? t("acl.group")
                        : t("acl.token")}
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
                        <Trans
                          t={t}
                          i18nKey="acl.tokenHint"
                          components={{ code: <code className="font-mono" /> }}
                        />
                      </p>
                    </>
                  ) : (
                    <Select value={subject} onValueChange={setSubject}>
                      <SelectTrigger id="acl-subject">
                        <SelectValue
                          placeholder={t("acl.selectSubject", {
                            context: subjectKind,
                          })}
                        />
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
                  {t("acl.propagateToChildren")}
                </label>

                {error && <p className="text-sm text-destructive">{error}</p>}

                {grantConflict && (
                  <SelfCredentialConfirm
                    title={t("acl.grantConflictTitle")}
                    message={grantConflict.message}
                    confirmValue={grantConflict.subject}
                    actionLabel={t("acl.grantAnyway")}
                    pending={updateACL.isPending}
                    onCancel={() => {
                      setGrantConflict(null);
                    }}
                    onConfirm={() => {
                      sendGrant(
                        grantConflict.input,
                        grantConflict.subject,
                        true,
                      );
                    }}
                  />
                )}

                <DialogFooter>
                  <Button
                    type="submit"
                    disabled={
                      !path.trim() ||
                      !role ||
                      !subject.trim() ||
                      updateACL.isPending
                    }
                  >
                    {updateACL.isPending ? t("acl.granting") : t("acl.grant")}
                  </Button>
                </DialogFooter>
              </form>
            </DialogContent>
          </Dialog>
        ) : undefined
      }
    >
      <>
        {!capabilities.loading && !capabilities.canModifyACL && (
          <p className="max-w-[75ch] px-[18px] pb-3 text-[12.5px] text-muted-foreground">
            <Trans
              t={t}
              i18nKey="acl.readOnly"
              components={{ code: <code className="font-mono" /> }}
            />
          </p>
        )}
        {revokeError && (
          <div className="mx-[18px] mb-3 flex items-start justify-between gap-2 rounded-[8px] bg-destructive/[0.08] px-3.5 py-2.5">
            <p className="min-w-0 text-sm [overflow-wrap:anywhere]">
              {revokeError}
            </p>
            <Button
              variant="ghost"
              size="sm"
              className="h-7 shrink-0"
              onClick={() => {
                setRevokeError("");
              }}
            >
              {t("acl.dismiss")}
            </Button>
          </div>
        )}

        {aclQuery.isLoading ? (
          <div className="px-[18px] pb-1">
            <Skeleton className="h-24 w-full" />
          </div>
        ) : !aclQuery.data || aclQuery.data.length === 0 ? (
          <p className="px-[18px] pb-1 text-sm text-muted-foreground">
            {t("acl.none")}
          </p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("acl.path")}</TableHead>
                <TableHead>{t("common:type")}</TableHead>
                <TableHead>{t("acl.subject")}</TableHead>
                <TableHead>{t("acl.role")}</TableHead>
                <TableHead>{t("acl.propagate")}</TableHead>
                {manageable && (
                  <TableHead className="w-0">
                    <span className="sr-only">{t("common:actions")}</span>
                  </TableHead>
                )}
              </TableRow>
            </TableHeader>
            <TableBody>
              {aclQuery.data.map((entry) => (
                <TableRow key={entryKey(entry)}>
                  <TableCell className="font-mono text-[12.5px] font-medium">
                    {entry.path}
                  </TableCell>
                  <TableCell className="text-muted-foreground">
                    {entry.type}
                  </TableCell>
                  <TableCell className="font-mono text-[12.5px]">
                    {entry.ugid}
                  </TableCell>
                  <TableCell>{entry.roleid}</TableCell>
                  <TableCell className="text-muted-foreground">
                    {entry.propagate ? t("common:yes") : t("common:no")}
                  </TableCell>
                  {manageable && (
                    <TableCell className="py-1 text-right">
                      <Button
                        variant="ghost"
                        size="icon"
                        className="h-8 w-8 text-muted-foreground hover:text-destructive"
                        aria-label={t("acl.revokeLabel", {
                          role: entry.roleid,
                          path: entry.path,
                        })}
                        onClick={() => {
                          setRevokeTarget(entry);
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

        <AlertDialog
          open={revokeTarget !== null}
          onOpenChange={(open) => {
            if (!open) setRevokeTarget(null);
          }}
        >
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle>{t("acl.revokeTitle")}</AlertDialogTitle>
              <AlertDialogDescription>
                {revokeTarget && (
                  <Trans
                    t={t}
                    i18nKey="acl.revokeDescription"
                    values={{
                      subject: revokeTarget.ugid,
                      role: revokeTarget.roleid,
                      path: revokeTarget.path,
                    }}
                    components={{
                      code: <code className="font-mono" />,
                      strong: <strong />,
                    }}
                  />
                )}
              </AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel>{t("common:cancel")}</AlertDialogCancel>
              <AlertDialogAction
                className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
                disabled={updateACL.isPending}
                onClick={(e: React.MouseEvent) => {
                  e.preventDefault();
                  if (revokeTarget) handleRevoke(revokeTarget, false);
                }}
              >
                {updateACL.isPending ? t("acl.revoking") : t("acl.revoke")}
              </AlertDialogAction>
            </AlertDialogFooter>
          </AlertDialogContent>
        </AlertDialog>

        {revokeConflict && (
          <SelfCredentialConfirm
            key={entryKey(revokeConflict.entry)}
            title={t("acl.revokeConflictTitle")}
            message={revokeConflict.message}
            confirmValue={revokeConflict.entry.ugid}
            actionLabel={t("acl.revoke")}
            pending={updateACL.isPending}
            onCancel={() => {
              setRevokeConflict(null);
            }}
            onConfirm={() => {
              handleRevoke(revokeConflict.entry, true);
            }}
          />
        )}
      </>
    </Panel>
  );
}
