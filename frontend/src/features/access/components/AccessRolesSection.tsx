import { useMemo, useRef, useState } from "react";
import { Lock, Plus, Trash2 } from "lucide-react";

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
  type AccessRole,
  useAccessRoles,
  useCreateAccessRole,
  useDeleteAccessRole,
  useUpdateAccessRole,
} from "../api/access-queries";

interface Props {
  clusterId: string;
  capabilities: AccessCapabilities;
}

/** The role editor: a draft of a new role, or of an existing one. */
interface RoleEditor {
  roleid: string;
  privs: string[];
  isNew: boolean;
  /** Which opening this editor is: see `openEditor`. The edits made in it keep it. */
  openingId: number;
}

/**
 * Whether `open` is the editor that opening `next` is for: the Create Role
 * editor for Create Role, the editor of the same role for an Edit. A Create Role
 * editor is never a role's, whatever id has been typed into it.
 */
function isEditorFor(
  open: RoleEditor | null,
  next: Pick<RoleEditor, "roleid" | "isNew">,
): boolean {
  if (open === null || open.isNew !== next.isNew) return false;
  return next.isNew || open.roleid === next.roleid;
}

/**
 * Every privilege the cluster knows about, grouped by category.
 *
 * Derived at runtime from the built-in Administrator role rather than a
 * hardcoded list: Proxmox defines Administrator as the complete set of valid
 * privileges, so this stays correct as Proxmox adds new ones across releases.
 * Falls back to the union of all roles if Administrator is somehow absent.
 */
function usePrivilegeCatalogue(
  roles: AccessRole[] | undefined,
): Record<string, string[]> {
  return useMemo(() => {
    if (!roles || roles.length === 0) return {};
    const admin = roles.find((r) => r.roleid === "Administrator");
    const all = admin?.privs
      ? admin.privs.split(",")
      : [...new Set(roles.flatMap((r) => (r.privs ? r.privs.split(",") : [])))];

    const grouped: Record<string, string[]> = {};
    for (const priv of all.filter(Boolean).sort()) {
      // Privileges are "Category.Action" (e.g. "VM.Audit"); anything without a
      // dot is grouped under Other rather than dropped, so a privilege from a
      // future Proxmox release still reaches the picker.
      const [category] = priv.split(".");
      (grouped[category || "Other"] ??= []).push(priv);
    }
    return grouped;
  }, [roles]);
}

export function AccessRolesSection({ clusterId, capabilities }: Props) {
  const { canManage } = useAuth();
  const rolesQuery = useAccessRoles(clusterId);
  const createRole = useCreateAccessRole(clusterId);
  const updateRole = useUpdateAccessRole(clusterId);
  const deleteRole = useDeleteAccessRole(clusterId);

  const catalogue = usePrivilegeCatalogue(rolesQuery.data);
  const [editing, setEditing] = useState<RoleEditor | null>(null);
  // The openings so far. Every editor that is opened takes the next one, and the
  // save of an editor is held to its number (see `settle`).
  const openings = useRef(0);
  const [error, setError] = useState("");
  const [deleteTarget, setDeleteTarget] = useState<string | null>(null);

  const manageable = canManage("access") && capabilities.canModifyRoles;

  // Both hooks opt out of the global error toast, because the open editor shows
  // its failure itself; the save settles through its promise, so that one that
  // settles after the editor has gone is toasted instead of lost (see
  // useSaveOutcome). This section opens one editor after another, and `error`
  // lives here, not in the editor, so a failure let through after its editor
  // was closed would show in the next editor opened — a role that never failed.
  // An editor is therefore told apart by its OPENING: each editor that is opened
  // takes the next number, which the edits made in it keep, and a save is held to
  // the number it went out under. One editor can take the place of another with
  // nothing closed between them: the editor is not held while the request is
  // out, so Tab walks out of it to the Edit buttons and Create Role behind the
  // modal, which a pointer cannot reach, and Enter on one of them sets another
  // editor straight over this one.
  //
  // Opening the editor that is already open is not an opening, though (see
  // `openEditor`): Enter on the Edit button of the role being edited, or on
  // Create Role over a Create Role editor, leaves that editor as it is, draft and
  // all. A save that is out is then the live editor's own, so a failure shows
  // there and a success closes it. A second editor in its place, seeded from the
  // list as it was while that save was out, would still hold the old privileges
  // once the save had gone through, and saving it would write them back. (While
  // that holds, the role the editor is for, or "new", tells editors apart just as
  // well as the number does; the number does not depend on it holding.)
  const settle = useSaveOutcome(editing === null ? null : editing.openingId);

  /**
   * Opens the editor `next` describes, as a new opening — unless that is the
   * editor already open, which stays as it is.
   */
  const openEditor = (next: Omit<RoleEditor, "openingId">) => {
    // Taken here and not in the updater, which React may run twice. A number
    // that goes unused, when the editor stays, costs nothing.
    const openingId = ++openings.current;
    setEditing((open) =>
      isEditorFor(open, next) ? open : { ...next, openingId },
    );
  };

  const handleSave = () => {
    if (!editing) return;
    setError("");
    const privs = editing.privs.join(",");
    const roleid = editing.isNew ? editing.roleid.trim() : editing.roleid;
    settle(
      editing.isNew
        ? createRole.mutateAsync({ roleid, privs })
        : updateRole.mutateAsync({ roleid, privs }),
      {
        action: `${editing.isNew ? "Creating" : "Saving"} role ${roleid}`,
        // Not once the editor has been dismissed: this would close whichever
        // one has been opened in its place.
        onSuccess: () => {
          setEditing(null);
        },
        onError: (err) => {
          setError(
            err instanceof ApiClientError ? err.message : "Failed to save role",
          );
        },
      },
    );
  };

  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between">
        <div>
          <CardTitle>Roles</CardTitle>
          {!capabilities.loading && !capabilities.canModifyRoles && (
            <p className="mt-1 text-xs text-muted-foreground">
              Read-only: Nexara&apos;s token lacks{" "}
              <code className="font-mono">Sys.Modify</code> on{" "}
              <code className="font-mono">/access</code>, which Proxmox requires
              to manage roles.
            </p>
          )}
        </div>
        {manageable && (
          <Button
            size="sm"
            onClick={() => {
              openEditor({ roleid: "", privs: [], isNew: true });
            }}
          >
            <Plus className="mr-2 h-4 w-4" />
            Create Role
          </Button>
        )}
      </CardHeader>

      <CardContent>
        {rolesQuery.isLoading ? (
          <Skeleton className="h-24 w-full" />
        ) : !rolesQuery.data || rolesQuery.data.length === 0 ? (
          <p className="text-sm text-muted-foreground">No roles found.</p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Role</TableHead>
                <TableHead>Privileges</TableHead>
                {manageable && (
                  <TableHead className="text-right">Actions</TableHead>
                )}
              </TableRow>
            </TableHeader>
            <TableBody>
              {rolesQuery.data.map((role) => {
                const count = role.privs
                  ? role.privs.split(",").filter(Boolean).length
                  : 0;
                return (
                  <TableRow key={role.roleid}>
                    <TableCell className="font-medium">
                      <span className="flex items-center gap-2">
                        {role.roleid}
                        {role.special && (
                          <Badge variant="secondary" className="gap-1">
                            <Lock className="h-3 w-3" />
                            Built-in
                          </Badge>
                        )}
                      </span>
                    </TableCell>
                    <TableCell className="text-muted-foreground">
                      {count} {count === 1 ? "privilege" : "privileges"}
                    </TableCell>
                    {manageable && (
                      <TableCell className="text-right">
                        {/* Built-in roles are immutable in Proxmox — offering
                            the controls would only produce a confusing 403.
                            Proxmox also admits a role named "." or "..",
                            which Nexara cannot address (lib/api-path.ts); that
                            row shows why in the same place, as text. */}
                        {role.special ? (
                          <span className="text-xs text-muted-foreground">
                            Immutable
                          </span>
                        ) : unaddressableHint(role.roleid) !== null ? (
                          <span className="text-xs text-muted-foreground">
                            {unaddressableHint(role.roleid)}
                          </span>
                        ) : (
                          <>
                            <Button
                              variant="ghost"
                              size="sm"
                              onClick={() => {
                                openEditor({
                                  roleid: role.roleid,
                                  privs: role.privs
                                    ? role.privs.split(",").filter(Boolean)
                                    : [],
                                  isNew: false,
                                });
                              }}
                            >
                              Edit
                            </Button>
                            <Button
                              variant="ghost"
                              size="sm"
                              aria-label={`Delete ${role.roleid}`}
                              onClick={() => {
                                setDeleteTarget(role.roleid);
                              }}
                            >
                              <Trash2 className="h-4 w-4 text-destructive" />
                            </Button>
                          </>
                        )}
                      </TableCell>
                    )}
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        )}

        {editing && (
          <Dialog
            open
            onOpenChange={(open) => {
              if (!open) {
                setEditing(null);
                setError("");
              }
            }}
          >
            <DialogContent className="max-w-2xl">
              <DialogHeader>
                <DialogTitle>
                  {editing.isNew ? "Create Role" : `Edit ${editing.roleid}`}
                </DialogTitle>
                <DialogDescription>
                  Privileges are read from this cluster, so the list matches its
                  Proxmox version.
                </DialogDescription>
              </DialogHeader>

              <div className="space-y-4">
                {editing.isNew && (
                  <div>
                    <Label htmlFor="role-id">Role ID</Label>
                    <Input
                      id="role-id"
                      value={editing.roleid}
                      onChange={(e) => {
                        setEditing({ ...editing, roleid: e.target.value });
                      }}
                      required
                    />
                  </div>
                )}

                <div className="max-h-80 space-y-3 overflow-y-auto pr-1">
                  {Object.entries(catalogue).map(([category, privs]) => (
                    <div key={category}>
                      <p className="mb-1 text-xs font-semibold text-muted-foreground">
                        {category}
                      </p>
                      <div className="grid grid-cols-2 gap-1 sm:grid-cols-3">
                        {privs.map((priv) => (
                          <label
                            key={priv}
                            className="flex items-center gap-2 text-xs"
                          >
                            <input
                              type="checkbox"
                              checked={editing.privs.includes(priv)}
                              onChange={(e) => {
                                setEditing({
                                  ...editing,
                                  privs: e.target.checked
                                    ? [...editing.privs, priv]
                                    : editing.privs.filter((p) => p !== priv),
                                });
                              }}
                            />
                            <span className="font-mono">{priv}</span>
                          </label>
                        ))}
                      </div>
                    </div>
                  ))}
                </div>

                <p className="text-xs text-muted-foreground">
                  {editing.privs.length} selected
                  {editing.privs.length === 0 && !editing.isNew && (
                    <span className="text-destructive">
                      {" "}
                      — saving with none selected removes every privilege from
                      this role.
                    </span>
                  )}
                </p>

                {error && <p className="text-sm text-destructive">{error}</p>}

                <div className="flex justify-end gap-2">
                  <Button
                    variant="outline"
                    onClick={() => {
                      setEditing(null);
                      setError("");
                    }}
                  >
                    Cancel
                  </Button>
                  <Button
                    onClick={handleSave}
                    disabled={
                      (editing.isNew && !editing.roleid.trim()) ||
                      createRole.isPending ||
                      updateRole.isPending
                    }
                  >
                    {createRole.isPending || updateRole.isPending
                      ? "Saving..."
                      : "Save"}
                  </Button>
                </div>
              </div>
            </DialogContent>
          </Dialog>
        )}

        <AlertDialog
          open={deleteTarget !== null}
          onOpenChange={(open) => {
            if (!open) setDeleteTarget(null);
          }}
        >
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle>Delete role {deleteTarget}?</AlertDialogTitle>
              <AlertDialogDescription>
                Any user or token holding this role loses the permissions it
                grants, immediately. This cannot be undone.
              </AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel>Cancel</AlertDialogCancel>
              <AlertDialogAction
                className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
                disabled={deleteRole.isPending}
                onClick={(e: React.MouseEvent) => {
                  e.preventDefault();
                  if (!deleteTarget) return;
                  const target = deleteTarget;
                  deleteRole.mutate(target, {
                    // Only this role's confirmation. It is not held while its
                    // request is out, so Tab walks out of it to the Delete
                    // buttons behind the modal, which a pointer cannot reach,
                    // and Enter on another role's opens that one in its place:
                    // this settling must not close it.
                    onSettled: () => {
                      setDeleteTarget((open) =>
                        open === target ? null : open,
                      );
                    },
                  });
                }}
              >
                {deleteRole.isPending ? "Deleting..." : "Delete Role"}
              </AlertDialogAction>
            </AlertDialogFooter>
          </AlertDialogContent>
        </AlertDialog>
      </CardContent>
    </Card>
  );
}
