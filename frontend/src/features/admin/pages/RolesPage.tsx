import { useState, useEffect } from "react";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Plus, Pencil, Trash2 } from "lucide-react";
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
        { onSuccess: () => { setEditRole(null); } },
      );
    } else {
      createRole.mutate(
        {
          name: formName,
          description: formDescription,
          permission_ids: selectedPermIds,
        },
        { onSuccess: () => { setIsCreateOpen(false); } },
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

  return (
    <div>
      <AdminNav />
      <div className="space-y-6 p-6">
        <div className="flex items-center justify-between">
          <div>
            <h1 className="text-2xl font-bold">Roles</h1>
            <p className="text-muted-foreground">
              Manage roles and their permissions
            </p>
          </div>
          <Button onClick={openCreate}>
            <Plus className="mr-2 h-4 w-4" />
            Create Role
          </Button>
        </div>

      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Name</TableHead>
            <TableHead>Description</TableHead>
            <TableHead>Type</TableHead>
            <TableHead className="w-24">Actions</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {roles?.map((role) => (
            <TableRow key={role.id}>
              <TableCell className="font-medium">{role.name}</TableCell>
              <TableCell className="text-muted-foreground">
                {role.description}
              </TableCell>
              <TableCell>
                <Badge variant={role.is_builtin ? "default" : "outline"}>
                  {role.is_builtin ? "Built-in" : "Custom"}
                </Badge>
              </TableCell>
              <TableCell>
                {!role.is_builtin && (
                  <div className="flex items-center gap-1">
                    <Button
                      variant="ghost"
                      size="icon"
                      onClick={() => { openEdit(role); }}
                    >
                      <Pencil className="h-4 w-4" />
                    </Button>
                    <Button
                      variant="ghost"
                      size="icon"
                      className="text-destructive hover:text-destructive"
                      onClick={() => {
                        if (
                          confirm(`Delete role "${role.name}"?`)
                        ) {
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

      <Dialog
        open={dialogOpen}
        onOpenChange={(open) => {
          if (!open) {
            setIsCreateOpen(false);
            setEditRole(null);
          }
        }}
      >
        <DialogContent className="max-w-[90vw] w-fit max-h-[90vh] overflow-y-auto">
          <DialogHeader>
            <DialogTitle>
              {editRole ? `Edit Role: ${editRole.name}` : "Create Role"}
            </DialogTitle>
          </DialogHeader>

          <div className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="role-name">Name</Label>
              <Input
                id="role-name"
                value={formName}
                onChange={(e) => { setFormName(e.target.value); }}
                placeholder="e.g. Network Admin"
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="role-desc">Description</Label>
              <Input
                id="role-desc"
                value={formDescription}
                onChange={(e) => { setFormDescription(e.target.value); }}
                placeholder="What this role can do"
              />
            </div>

            {allPermissions && (
              <div className="space-y-2">
                <Label>Permissions</Label>
                <PermissionMatrix
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
              {editRole ? "Save Changes" : "Create Role"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
      </div>
    </div>
  );
}
