import { useState } from "react";
import { useTranslation } from "react-i18next";
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
import { DateTime } from "@/components/DateTime";
import { MoreHorizontal, UserPlus } from "lucide-react";
import {
  useUsers,
  useUpdateUser,
  useDeleteUser,
  useResetSSOLink,
  useResetDirectoryLink,
} from "../api/rbac-queries";
import { useAdminResetTOTP } from "@/features/settings/api/totp-queries";
import { RoleAssignDialog } from "../components/RoleAssignDialog";
import { CreateUserDialog } from "../components/CreateUserDialog";
import { AdminNav } from "../components/AdminNav";
import { SYSTEM_USER_ID } from "@/lib/constants";
import { useAuthStore } from "@/stores/auth-store";

const SOURCE_LABEL_KEYS: Record<string, "local" | "ldap" | "oidc"> = {
  local: "local",
  ldap: "ldap",
  oidc: "oidc",
};

export function UsersPage() {
  const { t } = useTranslation("admin");
  const { data: rawUsers, isLoading } = useUsers();
  const users = rawUsers?.filter((u) => u.id !== SYSTEM_USER_ID);
  // Your own row offers nothing the server refuses on your own account:
  // the active switch, the resets and the delete.
  const me = useAuthStore((s) => s.user?.id);
  const updateUser = useUpdateUser();
  const deleteUser = useDeleteUser();
  const resetTotp = useAdminResetTOTP();
  const resetSSOLink = useResetSSOLink();
  const resetDirectoryLink = useResetDirectoryLink();
  const [roleDialogUserId, setRoleDialogUserId] = useState<string | null>(null);
  const [createDialogOpen, setCreateDialogOpen] = useState(false);

  if (isLoading) {
    return (
      <div>
        <AdminNav />
        <div className="flex h-64 items-center justify-center text-muted-foreground">
          {t("loadingUsers")}
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
          title={t("usersPage.title")}
          summary={t("usersPage.summary", {
            count: total,
            active,
            withTotp,
          })}
          actions={
            <Button
              onClick={() => {
                setCreateDialogOpen(true);
              }}
            >
              <UserPlus className="h-4 w-4" />
              {t("usersPage.createUser")}
            </Button>
          }
        />

        <Panel flush>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("usersPage.user")}</TableHead>
                <TableHead className="whitespace-nowrap">
                  {t("usersPage.signIn")}
                </TableHead>
                <TableHead>{t("twoFA")}</TableHead>
                <TableHead>{t("usersPage.roles")}</TableHead>
                <TableHead>{t("usersPage.active")}</TableHead>
                <TableHead>{t("usersPage.created")}</TableHead>
                <TableHead className="w-40">
                  <span className="sr-only">{t("common:actions")}</span>
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
                    {t(SOURCE_LABEL_KEYS[user.auth_source] ?? "local")}
                    {user.auth_source === "oidc" &&
                      user.sso_linked === false && (
                        <div className="whitespace-nowrap text-[12.5px]">
                          {t("usersPage.notLinkedYet")}
                        </div>
                      )}
                  </TableCell>
                  <TableCell>
                    {user.totp_enabled ? (
                      <Badge variant="healthy">{t("usersPage.on")}</Badge>
                    ) : (
                      <span className="text-muted-foreground">
                        {t("usersPage.off")}
                      </span>
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
                        <span className="text-muted-foreground">
                          {t("usersPage.noRoles")}
                        </span>
                      )}
                    </div>
                  </TableCell>
                  <TableCell>
                    <Switch
                      aria-label={t("usersPage.isActiveLabel", {
                        email: user.email,
                      })}
                      checked={user.is_active}
                      disabled={user.id === me}
                      onCheckedChange={(checked) => {
                        updateUser.mutate({ id: user.id, is_active: checked });
                      }}
                    />
                  </TableCell>
                  <TableCell className="whitespace-nowrap text-muted-foreground">
                    <DateTime value={user.created_at} kind="date" />
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
                        {t("manageRoles")}
                      </Button>
                      {user.id !== me && (
                        <DropdownMenu>
                          <DropdownMenuTrigger asChild>
                            <Button
                              aria-label={t("usersPage.moreActionsFor", {
                                email: user.email,
                              })}
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
                                      t("reset2FAConfirm", {
                                        email: user.email,
                                      }),
                                    )
                                  ) {
                                    resetTotp.mutate(user.id);
                                  }
                                }}
                              >
                                {t("reset2FA")}
                              </DropdownMenuItem>
                            )}
                            {user.auth_source === "oidc" &&
                              user.sso_linked === true && (
                                <DropdownMenuItem
                                  onClick={() => {
                                    if (
                                      confirm(
                                        t("usersPage.resetSSOLinkConfirm", {
                                          email: user.email,
                                        }),
                                      )
                                    ) {
                                      resetSSOLink.mutate(user.id);
                                    }
                                  }}
                                >
                                  {t("usersPage.resetSSOLink")}
                                </DropdownMenuItem>
                              )}
                            {user.auth_source === "ldap" &&
                              user.directory_linked === true && (
                                <DropdownMenuItem
                                  onClick={() => {
                                    if (
                                      confirm(
                                        t(
                                          "usersPage.resetDirectoryLinkConfirm",
                                          {
                                            email: user.email,
                                          },
                                        ),
                                      )
                                    ) {
                                      resetDirectoryLink.mutate(user.id);
                                    }
                                  }}
                                >
                                  {t("usersPage.resetDirectoryLink")}
                                </DropdownMenuItem>
                              )}
                            <DropdownMenuItem
                              className="text-destructive focus:text-destructive"
                              onClick={() => {
                                if (
                                  confirm(
                                    t("deleteUserConfirm", {
                                      email: user.email,
                                    }),
                                  )
                                ) {
                                  deleteUser.mutate(user.id);
                                }
                              }}
                            >
                              {t("deleteUser")}
                            </DropdownMenuItem>
                          </DropdownMenuContent>
                        </DropdownMenu>
                      )}
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
