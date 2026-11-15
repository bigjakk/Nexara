import { useState, useEffect } from "react";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Button } from "@/components/ui/button";
import { PageHeader } from "@/components/page/PageHeader";
import { Panel } from "@/components/page/Panel";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Plus, Trash2 } from "lucide-react";
import {
  useRoles,
  useRole,
  useCreateRole,
  useUpdateRole,
  useDeleteRole,
  usePermissionCatalog,
} from "../api/rbac-queries";
import { PermissionMatrix } from "../components/PermissionMatrix";
import { AdminNav } from "../components/AdminNav";
import type { RBACRole } from "@/types/api";

export function RolesPage() {
  const { data: roles, isLoading } = useRoles();
  const createRole = useCreateRole();
  const updateRole = useUpdateRole();
  const deleteRole = useDeleteRole();
  const { data: allPermissions } = usePermissionCatalog();

  const [editRole, setEditRole] = useState<RBACRole | null>(null);
  const [isCreateOpen, setIsCreateOpen] = useState(false);
  const [formName, setFormName] = useState("");
  const [formDescription, setFormDescription] = useState("");
  const [selectedPermIds, setSelectedPermIds] = useState<string[]>([]);

  // Fetch full role details (including permissions) when editing
  const { data: editRoleDetail } = useRole(editRole?.id ?? "");

  useEffect(() => {
    if (editRoleDetail?.permissions) {
      setSelectedPermIds(editRoleDetail.permissions.map((p) => p.id));
    }
  }, [editRoleDetail]);

  const openCreate = () => {
    setFormName("");
    setFormDescription("");
    setSelectedPermIds([]);
    setIsCreateOpen(true);
  };

  const openEdit = (role: RBACRole) => {
    setEditRole(role);
    setFormName(role.name);
    setFormDescription(role.description);
    setSelectedPermIds(role.permissions?.map((p) => p.id) ?? []);
  };

  const handleSave = () => {
    if (editRole) {
      updateRole.mutate(
        {
          id: editRole.id,
          name: formName,
          description: formDescription,
          permission_ids: selectedPermIds,
        },
        {
          onSuccess: () => {
            setEditRole(null);
          },
        },
      );
    } else {
      createRole.mutate(
        {
          name: formName,
          description: formDescription,
          permission_ids: selectedPermIds,
        },
        {
          onSuccess: () => {
            setIsCreateOpen(false);
          },
        },
      );
    }
  };

  if (isLoading) {
    return (
      <div>
        <AdminNav />
        <div className="flex h-64 items-center justify-center text-muted-foreground">
          Loading roles...
        </div>
      </div>
    );
  }

  const dialogOpen = isCreateOpen || !!editRole;
  const total = roles?.length ?? 0;
  const builtin = roles?.filter((r) => r.is_builtin).length ?? 0;

  return (
    <div>
      <AdminNav />
      <div className="space-y-5">
        <PageHeader
          title="Roles"
          summary={`${String(total)} role${total !== 1 ? "s" : ""}: ${String(builtin)} built in, ${String(total - builtin)} custom. A built-in role cannot be changed.`}
          actions={
            <Button onClick={openCreate}>
              <Plus className="h-4 w-4" />
              Create role
            </Button>
          }
        />

        <Panel flush>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Description</TableHead>
                <TableHead>Type</TableHead>
                <TableHead className="w-32">
                  <span className="sr-only">Actions</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {roles?.map((role) => (
                <TableRow key={role.id}>
                  <TableCell className="whitespace-nowrap font-medium">
                    {role.name}
                  </TableCell>
                  <TableCell className="min-w-[14rem] text-muted-foreground">
                    {role.description}
                  </TableCell>
                  <TableCell
                    className={
                      role.is_builtin
                        ? "whitespace-nowrap text-muted-foreground"
                        : "whitespace-nowrap"
                    }
                  >
                    {role.is_builtin ? "Built-in" : "Custom"}
                  </TableCell>
                  <TableCell>
                    {!role.is_builtin && (
                      <div className="flex items-center justify-end gap-1">
                        <Button
                          aria-label={`Edit ${role.name}`}
                          variant="ghost"
                          size="sm"
                          onClick={() => {
                            openEdit(role);
                          }}
                        >
                          Edit
                        </Button>
                        <Button
                          aria-label={`Delete ${role.name}`}
                          variant="ghost"
                          size="icon"
                          className="h-8 w-8 text-muted-foreground hover:text-destructive"
                          onClick={() => {
                            if (confirm(`Delete role "${role.name}"?`)) {
                              deleteRole.mutate(role.id);
                            }
                          }}
                        >
                          <Trash2 className="h-4 w-4" />
                        </Button>
                      </div>
                    )}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </Panel>

        <Dialog
          open={dialogOpen}
          onOpenChange={(open) => {
            if (!open) {
              setIsCreateOpen(false);
              setEditRole(null);
            }
          }}
        >
          <DialogContent className="max-h-[90vh] w-fit max-w-[90vw] overflow-y-auto">
            <DialogHeader>
              <DialogTitle>
                {editRole ? `Edit role: ${editRole.name}` : "Create role"}
              </DialogTitle>
            </DialogHeader>

            <div className="space-y-4">
              <div className="grid gap-4 sm:grid-cols-2">
                <div className="space-y-2">
                  <Label htmlFor="role-name">Name</Label>
                  <Input
                    id="role-name"
                    value={formName}
                    onChange={(e) => {
                      setFormName(e.target.value);
                    }}
                    placeholder="e.g. Network Admin"
                  />
                </div>
                <div className="space-y-2">
                  <Label htmlFor="role-desc">Description</Label>
                  <Input
                    id="role-desc"
                    value={formDescription}
                    onChange={(e) => {
                      setFormDescription(e.target.value);
                    }}
                    placeholder="What this role can do"
                  />
                </div>
              </div>

              {allPermissions && (
                <div className="space-y-2">
                  <div className="flex items-baseline justify-between gap-3">
                    <Label>Permissions</Label>
                    <span className="text-[12.5px] text-muted-foreground">
                      {/* Only ids still in the catalogue: a role can hold
                          one for a permission that has since gone. */}
                      {
                        selectedPermIds.filter((id) =>
                          allPermissions.some((p) => p.id === id),
                        ).length
                      }{" "}
                      of {allPermissions.length} granted
                    </span>
                  </div>
                  <PermissionMatrix
                    className="-mx-6"
                    permissions={allPermissions}
                    selected={selectedPermIds}
                    onChange={setSelectedPermIds}
                  />
                </div>
              )}
            </div>

            <DialogFooter>
              <Button
                variant="outline"
                onClick={() => {
                  setIsCreateOpen(false);
                  setEditRole(null);
                }}
              >
                Cancel
              </Button>
              <Button
                onClick={handleSave}
                disabled={
                  !formName || createRole.isPending || updateRole.isPending
                }
              >
                {editRole ? "Save changes" : "Create role"}
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      </div>
    </div>
  );
}
