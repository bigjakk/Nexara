import { useState, useEffect } from "react";
import { useTranslation } from "react-i18next";
import {
  User,
  Mail,
  Shield,
  Key,
  Calendar,
  Loader2,
  Check,
  Eye,
  EyeOff,
  LogOut,
} from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Separator } from "@/components/ui/separator";
import { ApiClientError } from "@/lib/api-client";
import {
  useProfile,
  useUpdateProfile,
  useChangePassword,
} from "../api/profile-queries";
import { useAuthStore } from "@/stores/auth-store";

const AUTH_SOURCE_LABELS: Record<string, string> = {
  local: "Local",
  ldap: "LDAP / Active Directory",
  oidc: "OIDC / SSO",
};

export function ProfilePage() {
  const { t } = useTranslation("common");
  const { data: profile, isLoading } = useProfile();
  const updateProfile = useUpdateProfile();
  const changePassword = useChangePassword();
  const clearAuth = useAuthStore((s) => s.clearAuth);

  const [displayName, setDisplayName] = useState("");
  const [profileSaved, setProfileSaved] = useState(false);
  const [profileError, setProfileError] = useState("");

  const [passwordDialogOpen, setPasswordDialogOpen] = useState(false);
  const [oldPassword, setOldPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [showOld, setShowOld] = useState(false);
  const [showNew, setShowNew] = useState(false);
  const [passwordError, setPasswordError] = useState("");
  const [passwordSuccess, setPasswordSuccess] = useState(false);

  const isLocal = profile?.auth_source === "local";

  useEffect(() => {
    if (profile) {
      setDisplayName(profile.display_name);
    }
  }, [profile]);

  const handleSaveProfile = () => {
    setProfileError("");
    setProfileSaved(false);
    updateProfile.mutate(
      { display_name: displayName },
      {
        onSuccess: () => {
          setProfileSaved(true);
          setTimeout(() => { setProfileSaved(false); }, 3000);
        },
        onError: (err) => {
          setProfileError(
            err instanceof ApiClientError ? err.message : "Failed to update profile",
          );
        },
      },
    );
  };

  const handleChangePassword = () => {
    setPasswordError("");
    setPasswordSuccess(false);

    if (newPassword !== confirmPassword) {
      setPasswordError("New passwords do not match");
      return;
    }

    changePassword.mutate(
      { old_password: oldPassword, new_password: newPassword },
      {
        onSuccess: () => {
          setPasswordSuccess(true);
          // Server revoked all sessions — force local logout after brief delay.
          setTimeout(() => {
            clearAuth();
          }, 2000);
        },
        onError: (err) => {
          setPasswordError(
            err instanceof ApiClientError ? err.message : "Failed to change password",
          );
        },
      },
    );
  };

  const resetPasswordDialog = () => {
    setOldPassword("");
    setNewPassword("");
    setConfirmPassword("");
    setPasswordError("");
    setPasswordSuccess(false);
    setShowOld(false);
    setShowNew(false);
  };

  if (isLoading) {
    return (
      <div className="flex items-center justify-center py-12">
        <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
      </div>
    );
  }

  if (!profile) return null;

  const displayNameChanged = displayName.trim() !== profile.display_name;

  return (
    <div className="mx-auto max-w-2xl space-y-6">
      <div>
        <h1 className="text-2xl font-semibold">{t("profile")}</h1>
        <p className="text-sm text-muted-foreground">
          Manage your account settings
        </p>
      </div>

      {/* Account Information */}
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <User className="h-5 w-5" />
            Account Information
          </CardTitle>
          <CardDescription>
            {isLocal
              ? "Your account details. You can update your display name below."
              : `Your account is managed by ${AUTH_SOURCE_LABELS[profile.auth_source] ?? profile.auth_source}. Profile changes must be made in your identity provider.`}
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="display-name">Display Name</Label>
            {isLocal ? (
              <Input
                id="display-name"
                value={displayName}
                onChange={(e) => { setDisplayName(e.target.value); }}
                maxLength={200}
              />
            ) : (
              <div className="flex h-9 items-center rounded-md border bg-muted px-3 text-sm">
                {profile.display_name}
              </div>
            )}
          </div>

          <div className="space-y-2">
            <Label>Email</Label>
            <div className="flex items-center gap-2">
              <Mail className="h-4 w-4 text-muted-foreground" />
              <span className="text-sm">{profile.email}</span>
            </div>
          </div>

          <div className="grid grid-cols-2 gap-4">
            <div className="space-y-2">
              <Label>Role</Label>
              <div className="flex items-center gap-2">
                <Shield className="h-4 w-4 text-muted-foreground" />
                <Badge variant="outline" className="capitalize">
                  {profile.role}
                </Badge>
              </div>
            </div>
            <div className="space-y-2">
              <Label>Auth Source</Label>
              <div>
                <Badge variant="secondary">
                  {AUTH_SOURCE_LABELS[profile.auth_source] ?? profile.auth_source}
                </Badge>
              </div>
            </div>
          </div>

          <div className="space-y-2">
            <Label>Two-Factor Authentication</Label>
            <div>
              {profile.totp_enabled ? (
                <Badge className="bg-green-600 hover:bg-green-700">Enabled</Badge>
              ) : (
                <Badge variant="outline">Not configured</Badge>
              )}
            </div>
          </div>

          <div className="space-y-2">
            <Label>Member Since</Label>
            <div className="flex items-center gap-2">
              <Calendar className="h-4 w-4 text-muted-foreground" />
              <span className="text-sm">
                {new Date(profile.created_at).toLocaleDateString(undefined, {
                  year: "numeric",
                  month: "long",
                  day: "numeric",
                })}
              </span>
            </div>
          </div>

          {isLocal && (
            <>
              <Separator />
              <div className="flex items-center gap-3">
                <Button
                  onClick={handleSaveProfile}
                  disabled={updateProfile.isPending || !displayNameChanged}
                >
                  {updateProfile.isPending ? (
                    <>
                      <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                      Saving...
                    </>
                  ) : (
                    "Save Changes"
                  )}
                </Button>
                {profileSaved && (
                  <span className="flex items-center gap-1 text-sm text-green-600">
                    <Check className="h-4 w-4" />
                    Saved
                  </span>
                )}
                {profileError && (
                  <span className="text-sm text-destructive">{profileError}</span>
                )}
              </div>
            </>
          )}
        </CardContent>
      </Card>

      {/* Password Section — local users only */}
      {isLocal && (
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <Key className="h-5 w-5" />
              Password
            </CardTitle>
            <CardDescription>
              Change your account password. You will need to enter your current
              password for verification.
            </CardDescription>
          </CardHeader>
          <CardContent>
            <Button
              variant="outline"
              onClick={() => {
                resetPasswordDialog();
                setPasswordDialogOpen(true);
              }}
            >
              Change Password
            </Button>
          </CardContent>
        </Card>
      )}

      {/* Change Password Dialog */}
      <Dialog open={passwordDialogOpen} onOpenChange={(open) => {
        if (!open) resetPasswordDialog();
        setPasswordDialogOpen(open);
      }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Change Password</DialogTitle>
            <DialogDescription>
              Enter your current password and choose a new one.
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-4 py-2">
            <div className="space-y-2">
              <Label htmlFor="old-password">Current Password</Label>
              <div className="relative">
                <Input
                  id="old-password"
                  type={showOld ? "text" : "password"}
                  value={oldPassword}
                  onChange={(e) => { setOldPassword(e.target.value); }}
                  autoComplete="current-password"
                />
                <Button
                  type="button"
                  variant="ghost"
                  size="icon"
                  className="absolute right-0 top-0 h-full px-3"
                  onClick={() => { setShowOld(!showOld); }}
                >
                  {showOld ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
                </Button>
              </div>
            </div>
            <div className="space-y-2">
              <Label htmlFor="new-password">New Password</Label>
              <div className="relative">
                <Input
                  id="new-password"
                  type={showNew ? "text" : "password"}
                  value={newPassword}
                  onChange={(e) => { setNewPassword(e.target.value); }}
                  autoComplete="new-password"
                />
                <Button
                  type="button"
                  variant="ghost"
                  size="icon"
                  className="absolute right-0 top-0 h-full px-3"
                  onClick={() => { setShowNew(!showNew); }}
                >
                  {showNew ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
                </Button>
              </div>
            </div>
            <div className="space-y-2">
              <Label htmlFor="confirm-password">Confirm New Password</Label>
              <Input
                id="confirm-password"
                type="password"
                value={confirmPassword}
                onChange={(e) => { setConfirmPassword(e.target.value); }}
                autoComplete="new-password"
              />
            </div>
            {passwordError && (
              <p className="text-sm text-destructive">{passwordError}</p>
            )}
            {passwordSuccess && (
              <p className="flex items-center gap-1 text-sm text-green-600">
                <LogOut className="h-4 w-4" />
                Password changed. Signing you out...
              </p>
            )}
          </div>
          <DialogFooter>
            <Button
              variant="outline"
              onClick={() => { setPasswordDialogOpen(false); }}
            >
              Cancel
            </Button>
            <Button
              onClick={handleChangePassword}
              disabled={
                changePassword.isPending ||
                !oldPassword ||
                !newPassword ||
                !confirmPassword ||
                passwordSuccess
              }
            >
              {changePassword.isPending ? (
                <>
                  <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                  Changing...
                </>
              ) : (
                "Change Password"
              )}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
