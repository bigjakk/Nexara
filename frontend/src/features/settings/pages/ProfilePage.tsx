import { useState, useEffect } from "react";
import { useTranslation } from "react-i18next";
import { Loader2, Check, Eye, EyeOff, LogOut } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { PageHeader } from "@/components/page/PageHeader";
import {
  SettingRow,
  SettingsFooter,
  SettingsSection,
} from "@/components/page/Settings";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { ApiClientError, sessionScope } from "@/lib/api-client";
import {
  useProfile,
  useUpdateProfile,
  useChangePassword,
} from "../api/profile-queries";
import { useAuthStore } from "@/stores/auth-store";
import { useSaveOutcome } from "@/hooks/useSaveOutcome";

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
  const settleProfile = useSaveOutcome();

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
  const settlePassword = useSaveOutcome(passwordDialogOpen);

  const isLocal = profile?.auth_source === "local";

  useEffect(() => {
    if (profile) {
      setDisplayName(profile.display_name);
    }
  }, [profile]);

  const handleSaveProfile = () => {
    setProfileError("");
    setProfileSaved(false);
    settleProfile(updateProfile.mutateAsync({ display_name: displayName }), {
      action: "Saving your display name",
      onSuccess: () => {
        setProfileSaved(true);
        setTimeout(() => {
          setProfileSaved(false);
        }, 3000);
      },
      onError: (err: unknown) => {
        setProfileError(
          err instanceof ApiClientError
            ? err.message
            : "Failed to update profile",
        );
      },
    });
  };

  const handleChangePassword = () => {
    setPasswordError("");
    setPasswordSuccess(false);

    if (newPassword !== confirmPassword) {
      setPasswordError("New passwords do not match");
      return;
    }

    // Server revoked all sessions — force local logout after brief delay.
    // The timer outlives this page, and is not cleared when it is left:
    // the session is revoked either way. But it is for the session that
    // changed the password: if that has ended by then — signed out, or
    // someone else signed in — clearAuth() would end theirs, or turn the
    // user's own Sign out into an expiry (the login page then carries the
    // page they left to whoever signs in next).
    const signOut = () => {
      const ended = sessionScope();
      setTimeout(() => {
        if (!ended()) clearAuth();
      }, 2000);
    };
    settlePassword(
      changePassword.mutateAsync({
        old_password: oldPassword,
        new_password: newPassword,
      }),
      {
        action: "Changing your password",
        onSuccess: () => {
          setPasswordSuccess(true);
          signOut();
        },
        // The dialog was closed while it was out: the sessions are revoked all
        // the same.
        onLateSuccess: signOut,
        onError: (err: unknown) => {
          setPasswordError(
            err instanceof ApiClientError
              ? err.message
              : "Failed to change password",
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

  const sourceLabel =
    AUTH_SOURCE_LABELS[profile.auth_source] ?? profile.auth_source;
  const memberSince = new Date(profile.created_at).toLocaleDateString(
    undefined,
    { year: "numeric", month: "long", day: "numeric" },
  );

  return (
    <div className="space-y-5">
      <PageHeader
        title={t("profile")}
        summary={`${sourceLabel} account for ${profile.email}, created ${memberSince}.`}
      />

      <div className="max-w-3xl space-y-4">
        <SettingsSection
          title="Account"
          description={
            isLocal
              ? "Who you are in Nexara. Only the display name can be changed here."
              : `Your account is managed by ${sourceLabel}. Profile changes must be made in your identity provider.`
          }
        >
          <SettingRow
            label="Display name"
            description="Shown beside what you do in the audit log and in the top bar."
            {...(isLocal ? { htmlFor: "display-name" } : {})}
          >
            {isLocal ? (
              <Input
                id="display-name"
                value={displayName}
                onChange={(e) => {
                  setDisplayName(e.target.value);
                }}
                maxLength={200}
              />
            ) : (
              <span className="text-sm [overflow-wrap:anywhere]">
                {profile.display_name}
              </span>
            )}
          </SettingRow>
          <SettingRow label="Email" description="The address you sign in with.">
            <span className="text-sm [overflow-wrap:anywhere]">
              {profile.email}
            </span>
          </SettingRow>
          <SettingRow label="Role">
            <span className="text-sm capitalize">{profile.role}</span>
          </SettingRow>
          <SettingRow label="Sign-in method">
            <span className="text-sm">{sourceLabel}</span>
          </SettingRow>
          <SettingRow
            label="Two-factor authentication"
            description="Turned on and off under Security."
          >
            {profile.totp_enabled ? (
              <Badge variant="healthy">Enabled</Badge>
            ) : (
              <span className="text-sm text-muted-foreground">
                Not configured
              </span>
            )}
          </SettingRow>
          <SettingRow label="Member since">
            <span className="text-sm">{memberSince}</span>
          </SettingRow>
          {isLocal && (
            <SettingsFooter>
              {profileSaved && (
                <span className="flex items-center gap-1 text-sm text-muted-foreground">
                  <Check className="h-4 w-4" />
                  Saved
                </span>
              )}
              {profileError && (
                <span className="text-sm text-destructive">{profileError}</span>
              )}
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
                  "Save changes"
                )}
              </Button>
            </SettingsFooter>
          )}
        </SettingsSection>

        {/* Password: local accounts only */}
        {isLocal && (
          <SettingsSection title="Password">
            <SettingRow
              label="Account password"
              description="You are asked for the current one first. Changing it signs you out everywhere, this browser included."
            >
              <Button
                variant="outline"
                onClick={() => {
                  resetPasswordDialog();
                  setPasswordDialogOpen(true);
                }}
              >
                Change password
              </Button>
            </SettingRow>
          </SettingsSection>
        )}
      </div>

      {/* Change password dialog */}
      <Dialog
        open={passwordDialogOpen}
        onOpenChange={(open) => {
          if (!open) resetPasswordDialog();
          setPasswordDialogOpen(open);
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Change password</DialogTitle>
            <DialogDescription>
              Enter your current password and choose a new one.
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-4 py-2">
            <div className="space-y-2">
              <Label htmlFor="old-password">Current password</Label>
              <div className="relative">
                <Input
                  id="old-password"
                  type={showOld ? "text" : "password"}
                  value={oldPassword}
                  onChange={(e) => {
                    setOldPassword(e.target.value);
                  }}
                  autoComplete="current-password"
                />
                <Button
                  aria-label={
                    showOld ? "Hide current password" : "Show current password"
                  }
                  type="button"
                  variant="ghost"
                  size="icon"
                  className="absolute right-0 top-0 h-full px-3"
                  onClick={() => {
                    setShowOld(!showOld);
                  }}
                >
                  {showOld ? (
                    <EyeOff className="h-4 w-4" />
                  ) : (
                    <Eye className="h-4 w-4" />
                  )}
                </Button>
              </div>
            </div>
            <div className="space-y-2">
              <Label htmlFor="new-password">New password</Label>
              <div className="relative">
                <Input
                  id="new-password"
                  type={showNew ? "text" : "password"}
                  value={newPassword}
                  onChange={(e) => {
                    setNewPassword(e.target.value);
                  }}
                  autoComplete="new-password"
                />
                <Button
                  aria-label={
                    showNew ? "Hide new password" : "Show new password"
                  }
                  type="button"
                  variant="ghost"
                  size="icon"
                  className="absolute right-0 top-0 h-full px-3"
                  onClick={() => {
                    setShowNew(!showNew);
                  }}
                >
                  {showNew ? (
                    <EyeOff className="h-4 w-4" />
                  ) : (
                    <Eye className="h-4 w-4" />
                  )}
                </Button>
              </div>
            </div>
            <div className="space-y-2">
              <Label htmlFor="confirm-password">Confirm new password</Label>
              <Input
                id="confirm-password"
                type="password"
                value={confirmPassword}
                onChange={(e) => {
                  setConfirmPassword(e.target.value);
                }}
                autoComplete="new-password"
              />
            </div>
            {passwordError && (
              <p className="text-sm text-destructive">{passwordError}</p>
            )}
            {passwordSuccess && (
              <p className="flex items-center gap-1.5 text-sm">
                <LogOut className="h-4 w-4 text-muted-foreground" />
                Password changed. Signing you out...
              </p>
            )}
          </div>
          <DialogFooter>
            <Button
              variant="outline"
              onClick={() => {
                setPasswordDialogOpen(false);
              }}
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
                "Change password"
              )}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
