import { useState } from "react";
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
import { Switch } from "@/components/ui/switch";
import { Trash2, ShieldCheck, ShieldOff, UserPlus } from "lucide-react";
import { useUsers, useUpdateUser, useDeleteUser } from "../api/rbac-queries";
import { useAdminResetTOTP } from "@/features/settings/api/totp-queries";
import { RoleAssignDialog } from "../components/RoleAssignDialog";
import { CreateUserDialog } from "../components/CreateUserDialog";
import { AdminNav } from "../components/AdminNav";

export function UsersPage() {
  const { data: users, isLoading } = useUsers();
  const updateUser = useUpdateUser();
  const deleteUser = useDeleteUser();
  const resetTotp = useAdminResetTOTP();
  const [roleDialogUserId, setRoleDialogUserId] = useState<string | null>(null);
  const [createDialogOpen, setCreateDialogOpen] = useState(false);

  if (isLoading) {
    return (
      <div>
        <AdminNav />
        <div className="flex h-64 items-center justify-center text-muted-foreground">
          Loading users...
        </div>
      </div>
    );
  }

  return (
    <div>
      <AdminNav />
      <div className="space-y-6 p-6">
        <div className="flex items-center justify-between">
          <div>
            <h1 className="text-2xl font-bold">User Management</h1>
            <p className="text-muted-foreground">
              Manage user accounts and role assignments
            </p>
          </div>
          <Button onClick={() => { setCreateDialogOpen(true); }}>
            <UserPlus className="mr-2 h-4 w-4" />
            Create User
          </Button>
        </div>

      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Email</TableHead>
            <TableHead>Display Name</TableHead>
            <TableHead>Source</TableHead>
            <TableHead>2FA</TableHead>
            <TableHead>Roles</TableHead>
            <TableHead>Active</TableHead>
            <TableHead>Created</TableHead>
            <TableHead className="w-32">Actions</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {users?.map((user) => (
            <TableRow key={user.id}>
              <TableCell className="font-medium">{user.email}</TableCell>
              <TableCell>{user.display_name}</TableCell>
              <TableCell>
                <Badge variant={user.auth_source === "local" ? "secondary" : "outline"}>
                  {user.auth_source === "ldap" ? "LDAP" : user.auth_source === "oidc" ? "OIDC" : "Local"}
                </Badge>
              </TableCell>
              <TableCell>
                {user.totp_enabled ? (
                  <Badge variant="default" className="bg-green-600 hover:bg-green-700">
                    <ShieldCheck className="mr-1 h-3 w-3" />
                    Enabled
                  </Badge>
                ) : (
                  <span className="text-sm text-muted-foreground">Off</span>
                )}
              </TableCell>
              <TableCell>
                <div className="flex flex-wrap gap-1">
                  {user.roles.length > 0 ? (
                    user.roles.map((roleName) => (
                      <Badge key={roleName} variant={roleName === "Admin" ? "default" : "secondary"}>
                        {roleName}
                      </Badge>
                    ))
                  ) : (
                    <span className="text-sm text-muted-foreground">No roles</span>
                  )}
                </div>
              </TableCell>
              <TableCell>
                <Switch
                  checked={user.is_active}
                  onCheckedChange={(checked) => {
                    updateUser.mutate({ id: user.id, is_active: checked });
                  }}
                />
              </TableCell>
              <TableCell className="text-muted-foreground text-sm">
                {new Date(user.created_at).toLocaleDateString()}
              </TableCell>
              <TableCell>
                <div className="flex items-center gap-1">
                  <Button
                    variant="ghost"
                    size="icon"
                    onClick={() => { setRoleDialogUserId(user.id); }}
                    title="Manage roles"
                  >
                    <ShieldCheck className="h-4 w-4" />
                  </Button>
                  {user.totp_enabled && (
                    <Button
                      variant="ghost"
                      size="icon"
                      onClick={() => {
                        if (
                          confirm(
                            `Reset 2FA for ${user.email}? They will need to set up 2FA again.`,
                          )
                        ) {
                          resetTotp.mutate(user.id);
                        }
                      }}
                      title="Reset 2FA"
                      className="text-amber-600 hover:text-amber-700"
                    >
                      <ShieldOff className="h-4 w-4" />
                    </Button>
                  )}
                  <Button
                    variant="ghost"
                    size="icon"
                    onClick={() => {
                      if (
                        confirm(
                          `Delete user ${user.email}? This cannot be undone.`,
                        )
                      ) {
                        deleteUser.mutate(user.id);
                      }
                    }}
                    title="Delete user"
                    className="text-destructive hover:text-destructive"
                  >
                    <Trash2 className="h-4 w-4" />
                  </Button>
                </div>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>

      <CreateUserDialog
        open={createDialogOpen}
        onOpenChange={setCreateDialogOpen}
      />

      {roleDialogUserId && (
        <RoleAssignDialog
          userId={roleDialogUserId}
          open={!!roleDialogUserId}
          onOpenChange={(open) => {
            if (!open) setRoleDialogUserId(null);
          }}
        />
      )}
      </div>
    </div>
  );
}
