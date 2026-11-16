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
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { PageHeader } from "@/components/page/PageHeader";
import { Panel } from "@/components/page/Panel";
import { MoreHorizontal, UserPlus } from "lucide-react";
import {
  useUsers,
  useUpdateUser,
  useDeleteUser,
  useResetSSOLink,
} from "../api/rbac-queries";
import { useAdminResetTOTP } from "@/features/settings/api/totp-queries";
import { RoleAssignDialog } from "../components/RoleAssignDialog";
import { CreateUserDialog } from "../components/CreateUserDialog";
import { AdminNav } from "../components/AdminNav";
import { SYSTEM_USER_ID } from "@/lib/constants";

const SOURCE_LABELS: Record<string, string> = {
  local: "Local",
  ldap: "LDAP",
  oidc: "OIDC",
};

export function UsersPage() {
  const { data: rawUsers, isLoading } = useUsers();
  const users = rawUsers?.filter((u) => u.id !== SYSTEM_USER_ID);
  const updateUser = useUpdateUser();
  const deleteUser = useDeleteUser();
  const resetTotp = useAdminResetTOTP();
  const resetSSOLink = useResetSSOLink();
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

  const total = users?.length ?? 0;
  const active = users?.filter((u) => u.is_active).length ?? 0;
  const withTotp = users?.filter((u) => u.totp_enabled).length ?? 0;

  return (
    <div>
      <AdminNav />
      <div className="space-y-5">
        <PageHeader
          title="Users"
          summary={`${String(total)} account${total !== 1 ? "s" : ""}, ${String(active)} active, ${String(withTotp)} with two-factor authentication.`}
          actions={
            <Button
              onClick={() => {
                setCreateDialogOpen(true);
              }}
            >
              <UserPlus className="h-4 w-4" />
              Create user
            </Button>
          }
        />

        <Panel flush>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>User</TableHead>
                <TableHead className="whitespace-nowrap">Sign-in</TableHead>
                <TableHead>2FA</TableHead>
                <TableHead>Roles</TableHead>
                <TableHead>Active</TableHead>
                <TableHead>Created</TableHead>
                <TableHead className="w-40">
                  <span className="sr-only">Actions</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {users?.map((user) => (
                <TableRow key={user.id}>
                  <TableCell>
                    <div className="whitespace-nowrap font-medium">
                      {user.email}
                    </div>
                    {user.display_name !== "" && (
                      <div className="text-[12.5px] text-muted-foreground">
                        {user.display_name}
                      </div>
                    )}
                  </TableCell>
                  <TableCell className="text-muted-foreground">
                    {SOURCE_LABELS[user.auth_source] ?? "Local"}
                    {user.auth_source === "oidc" && !user.sso_linked && (
                      <div className="whitespace-nowrap text-[12.5px]">
                        Not linked yet
                      </div>
                    )}
                  </TableCell>
                  <TableCell>
                    {user.totp_enabled ? (
                      <Badge variant="healthy">On</Badge>
                    ) : (
                      <span className="text-muted-foreground">Off</span>
                    )}
                  </TableCell>
                  <TableCell>
                    <div className="flex flex-wrap gap-1">
                      {user.roles.length > 0 ? (
                        user.roles.map((roleName) => (
                          <Badge key={roleName} variant="secondary">
                            {roleName}
                          </Badge>
                        ))
                      ) : (
                        <span className="text-muted-foreground">No roles</span>
                      )}
                    </div>
                  </TableCell>
                  <TableCell>
                    <Switch
                      aria-label={`${user.email} is active`}
                      checked={user.is_active}
                      onCheckedChange={(checked) => {
                        updateUser.mutate({ id: user.id, is_active: checked });
                      }}
                    />
                  </TableCell>
                  <TableCell className="whitespace-nowrap text-muted-foreground">
                    {new Date(user.created_at).toLocaleDateString()}
                  </TableCell>
                  <TableCell>
                    <div className="flex items-center justify-end gap-1">
                      <Button
                        variant="ghost"
                        size="sm"
                        onClick={() => {
                          setRoleDialogUserId(user.id);
                        }}
                      >
                        Manage roles
                      </Button>
                      <DropdownMenu>
                        <DropdownMenuTrigger asChild>
                          <Button
                            aria-label={`More actions for ${user.email}`}
                            variant="ghost"
                            size="icon"
                            className="h-8 w-8"
                          >
                            <MoreHorizontal className="h-4 w-4" />
                          </Button>
                        </DropdownMenuTrigger>
                        <DropdownMenuContent align="end">
                          {user.totp_enabled && (
                            <DropdownMenuItem
                              onClick={() => {
                                if (
                                  confirm(
                                    `Reset 2FA for ${user.email}? They will need to set up 2FA again.`,
                                  )
                                ) {
                                  resetTotp.mutate(user.id);
                                }
                              }}
                            >
                              Reset 2FA
                            </DropdownMenuItem>
                          )}
                          {user.auth_source === "oidc" && user.sso_linked && (
                            <DropdownMenuItem
                              onClick={() => {
                                if (
                                  confirm(
                                    `Reset the SSO link of ${user.email}? Their sessions end now, and their next SSO sign-in links the account to whichever identity presents this address.`,
                                  )
                                ) {
                                  resetSSOLink.mutate(user.id);
                                }
                              }}
                            >
                              Reset SSO link
                            </DropdownMenuItem>
                          )}
                          <DropdownMenuItem
                            className="text-destructive focus:text-destructive"
                            onClick={() => {
                              if (
                                confirm(
                                  `Delete user ${user.email}? This cannot be undone.`,
                                )
                              ) {
                                deleteUser.mutate(user.id);
                              }
                            }}
                          >
                            Delete user
                          </DropdownMenuItem>
                        </DropdownMenuContent>
                      </DropdownMenu>
                    </div>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </Panel>

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
