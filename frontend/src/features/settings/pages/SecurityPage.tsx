import { useRef, useState } from "react";
import { QRCodeSVG } from "qrcode.react";
import { Copy, Download, Loader2, RefreshCw } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { PageHeader } from "@/components/page/PageHeader";
import { Panel } from "@/components/page/Panel";
import { SettingRow, SettingsSection } from "@/components/page/Settings";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { ApiClientError } from "@/lib/api-client";
import { ActiveSessionsCard } from "../components/ActiveSessionsCard";
import { useProfile } from "../api/profile-queries";
import {
  useTOTPStatus,
  useTOTPSetup,
  useTOTPConfirm,
  useTOTPDisable,
  useRegenerateRecoveryCodes,
} from "../api/totp-queries";

type SetupStep = "idle" | "qr" | "verify" | "recovery";

export function SecurityPage() {
  const { data: status, isLoading: statusLoading } = useTOTPStatus();
  // An account with no Nexara password (LDAP, single sign-on) has none to
  // confirm: the server asks it for a recent sign-in instead. Undecided until
  // the profile has loaded, so nobody is asked for a password they do not have.
  const { data: profile, isError: profileFailed } = useProfile();
  const hasPassword =
    profile === undefined ? undefined : profile.auth_source === "local";
  const setupMutation = useTOTPSetup();
  const confirmMutation = useTOTPConfirm();
  const disableMutation = useTOTPDisable();
  const regenMutation = useRegenerateRecoveryCodes();

  const [setupStep, setSetupStep] = useState<SetupStep>("idle");
  const [otpauthUrl, setOtpauthUrl] = useState("");
  const [manualSecret, setManualSecret] = useState("");
  const [verifyCode, setVerifyCode] = useState("");
  const [recoveryCodes, setRecoveryCodes] = useState<string[]>([]);
  const [error, setError] = useState("");

  // Enable 2FA first proves it is the owner, in a dialog of its own. Each close
  // moves setupAttempt on, so an answer that lands after the dialog was closed
  // is dropped instead of opening the QR step or an error nobody asked for.
  const [setupDialogOpen, setSetupDialogOpen] = useState(false);
  const [currentPassword, setCurrentPassword] = useState("");
  const [setupError, setSetupError] = useState("");
  const setupAttempt = useRef(0);

  const [disableDialogOpen, setDisableDialogOpen] = useState(false);
  // Where focus goes when Disable 2FA closes: the section, whose Disable 2FA
  // button goes once 2FA is off.
  const twoFactorCard = useRef<HTMLElement>(null);
  const [disableCode, setDisableCode] = useState("");

  const [regenDialogOpen, setRegenDialogOpen] = useState(false);
  const [regenCode, setRegenCode] = useState("");

  // The password is kept only while the dialog is open: here, and in the
  // mutation's variables, which reset() drops.
  const closeSetupDialog = () => {
    setupAttempt.current++;
    setSetupDialogOpen(false);
    setCurrentPassword("");
    setSetupError("");
    setupMutation.reset();
  };

  const handleStartSetup = async () => {
    setSetupError("");
    const attempt = setupAttempt.current;
    try {
      const res = await setupMutation.mutateAsync(
        hasPassword ? { current_password: currentPassword } : {},
      );
      if (attempt !== setupAttempt.current) return;
      setOtpauthUrl(res.otpauth_url);
      setManualSecret(res.secret);
      closeSetupDialog();
      setSetupStep("qr");
    } catch (err) {
      if (attempt !== setupAttempt.current) return;
      if (err instanceof ApiClientError) {
        setSetupError(err.body.message);
      } else {
        setSetupError("Failed to start setup");
      }
    }
  };

  const handleVerify = async () => {
    setError("");
    try {
      const res = await confirmMutation.mutateAsync(verifyCode);
      setRecoveryCodes(res.recovery_codes);
      setSetupStep("recovery");
      setVerifyCode("");
    } catch (err) {
      if (err instanceof ApiClientError) {
        setError(err.body.message);
      } else {
        setError("Verification failed");
      }
    }
  };

  const handleDisable = async () => {
    setError("");
    try {
      await disableMutation.mutateAsync({ code: disableCode });
      setDisableDialogOpen(false);
      setDisableCode("");
    } catch (err) {
      if (err instanceof ApiClientError) {
        setError(err.body.message);
      } else {
        setError("Failed to disable 2FA");
      }
    }
  };

  const handleRegenerate = async () => {
    setError("");
    try {
      const res = await regenMutation.mutateAsync(regenCode);
      setRecoveryCodes(res.recovery_codes);
      setRegenDialogOpen(false);
      setRegenCode("");
      setSetupStep("recovery");
    } catch (err) {
      if (err instanceof ApiClientError) {
        setError(err.body.message);
      } else {
        setError("Failed to regenerate codes");
      }
    }
  };

  const copyRecoveryCodes = () => {
    void navigator.clipboard.writeText(recoveryCodes.join("\n"));
  };

  const downloadRecoveryCodes = () => {
    const blob = new Blob(
      [
        "Nexara Recovery Codes\n",
        "========================\n",
        "Store these codes in a safe place.\n",
        "Each code can only be used once.\n\n",
        ...recoveryCodes.map((c) => c + "\n"),
      ],
      { type: "text/plain" },
    );
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = "nexara-recovery-codes.txt";
    a.click();
    URL.revokeObjectURL(url);
  };

  if (statusLoading) {
    return (
      <div className="flex h-64 items-center justify-center">
        <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
      </div>
    );
  }

  const enabled = status?.enabled === true;
  const codesLeft = status?.recovery_codes_remaining ?? 0;

  return (
    <div className="space-y-5">
      <PageHeader
        title="Security"
        summary={
          enabled
            ? `Two-factor authentication is on, with ${String(codesLeft)} recovery code${codesLeft !== 1 ? "s" : ""} left.`
            : "Two-factor authentication is off. Sign-in asks for nothing after your password or single sign-on."
        }
      />

      <div className="max-w-3xl space-y-4">
        {error && (
          <div className="rounded-md bg-destructive/10 p-3 text-sm text-red-700 dark:text-red-400">
            {error}
          </div>
        )}

        <SettingsSection
          ref={twoFactorCard}
          tabIndex={-1}
          className="outline-none"
          title="Two-factor authentication"
          description="A second step at sign-in: a 6-digit code from a TOTP authenticator app."
          actions={
            enabled ? (
              <Badge variant="healthy">On</Badge>
            ) : (
              <span className="text-sm text-muted-foreground">Off</span>
            )
          }
        >
          {enabled ? (
            <>
              <SettingRow
                label="Recovery codes"
                description={
                  <>
                    {codesLeft} recovery code
                    {codesLeft !== 1 ? "s" : ""} remaining. Each one signs you
                    in once if you lose your authenticator.
                    {codesLeft < 3 && (
                      <span className="mt-1 block text-amber-700 dark:text-amber-400">
                        You have few recovery codes left. Consider regenerating
                        them.
                      </span>
                    )}
                  </>
                }
              >
                <Button
                  variant="outline"
                  onClick={() => {
                    setRegenDialogOpen(true);
                    setError("");
                  }}
                >
                  <RefreshCw className="h-4 w-4" />
                  Regenerate recovery codes
                </Button>
              </SettingRow>
              <SettingRow
                label="Turn off"
                description="Removes the second step from your sign-in. You are asked for a current code first."
              >
                <Button
                  variant="outline"
                  className="text-destructive hover:text-destructive"
                  onClick={() => {
                    setDisableDialogOpen(true);
                    setError("");
                  }}
                >
                  Disable 2FA
                </Button>
              </SettingRow>
            </>
          ) : setupStep === "idle" ? (
            <SettingRow
              label="Authenticator app"
              description="Not set up. Enabling it shows a QR code to scan, then asks for a first code to confirm."
            >
              <Button
                onClick={() => {
                  setError("");
                  setSetupDialogOpen(true);
                }}
              >
                Enable 2FA
              </Button>
            </SettingRow>
          ) : setupStep === "qr" ? (
            <div className="flex flex-wrap items-start gap-x-6 gap-y-4 pt-1">
              <div className="rounded-lg bg-white p-3 ring-1 ring-border">
                <QRCodeSVG value={otpauthUrl} size={176} />
              </div>
              <div className="min-w-0 flex-[1_1_16rem] space-y-4">
                <h3 className="text-sm font-medium">
                  Step 1: Scan the QR code with your authenticator app
                </h3>
                <div>
                  <p className="mb-1 text-[13px] text-muted-foreground">
                    Or enter this secret manually:
                  </p>
                  <code className="block select-all break-all rounded-md bg-foreground/[0.06] px-3 py-2 font-mono text-sm">
                    {manualSecret}
                  </code>
                </div>
                <Button
                  onClick={() => {
                    setSetupStep("verify");
                  }}
                >
                  Next
                </Button>
              </div>
            </div>
          ) : setupStep === "verify" ? (
            <div className="space-y-4 pt-1">
              <h3 className="text-sm font-medium">
                Step 2: Enter the 6-digit code from your authenticator
              </h3>
              <div className="max-w-xs space-y-2">
                <Label htmlFor="setup-verify-code">Verification code</Label>
                <Input
                  id="setup-verify-code"
                  type="text"
                  inputMode="numeric"
                  pattern="[0-9]*"
                  maxLength={6}
                  placeholder="000000"
                  autoComplete="one-time-code"
                  autoFocus
                  value={verifyCode}
                  onChange={(e) => {
                    setVerifyCode(e.target.value.replace(/\D/g, ""));
                  }}
                />
              </div>
              <div className="flex gap-2">
                <Button
                  onClick={() => void handleVerify()}
                  disabled={
                    verifyCode.length !== 6 || confirmMutation.isPending
                  }
                >
                  {confirmMutation.isPending && (
                    <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                  )}
                  Verify and enable
                </Button>
                <Button
                  variant="ghost"
                  onClick={() => {
                    setSetupStep("qr");
                  }}
                >
                  Back
                </Button>
              </div>
            </div>
          ) : null}
        </SettingsSection>

        {/* Recovery codes, shown once after enabling or regenerating */}
        {setupStep === "recovery" && recoveryCodes.length > 0 && (
          <Panel
            title="Recovery codes"
            actions={
              <>
                <Button variant="outline" size="sm" onClick={copyRecoveryCodes}>
                  <Copy className="h-4 w-4" />
                  Copy
                </Button>
                <Button
                  variant="outline"
                  size="sm"
                  onClick={downloadRecoveryCodes}
                >
                  <Download className="h-4 w-4" />
                  Download
                </Button>
              </>
            }
          >
            <p className="-mt-2 mb-3 max-w-[65ch] text-sm text-muted-foreground">
              Save these codes in a safe place. Each code can only be used once
              to sign in if you lose access to your authenticator app.
            </p>
            <div className="grid grid-cols-2 gap-x-6 gap-y-1 rounded-md bg-foreground/[0.06] px-4 py-3 font-mono text-sm">
              {recoveryCodes.map((code) => (
                <div key={code} className="py-1">
                  {code}
                </div>
              ))}
            </div>
            <div className="flex justify-end pt-3.5">
              <Button
                onClick={() => {
                  setSetupStep("idle");
                  setRecoveryCodes([]);
                }}
              >
                Done
              </Button>
            </div>
          </Panel>
        )}

        <ActiveSessionsCard />
      </div>

      {/* Enable 2FA dialog: the owner proves it is them before the secret is shown */}
      <Dialog
        open={setupDialogOpen}
        onOpenChange={(open) => {
          if (!open) closeSetupDialog();
        }}
      >
        <DialogContent fallbackFocus={() => twoFactorCard.current}>
          <DialogHeader>
            <DialogTitle>Enable two-factor authentication</DialogTitle>
            <DialogDescription>
              {hasPassword === undefined
                ? profileFailed
                  ? "Your account could not be loaded. Reload the page to set up 2FA."
                  : "Loading your account…"
                : hasPassword
                  ? "Enter your current password to continue. You will then scan a QR code with your authenticator app."
                  : "Your account signs in through LDAP or single sign-on, so there is no Nexara password to confirm. Setup needs a sign-in from the last 10 minutes: if yours is older, sign out and sign in again first."}
            </DialogDescription>
          </DialogHeader>
          {hasPassword && (
            <div className="space-y-2">
              <Label htmlFor="setup-current-password">Current password</Label>
              <Input
                id="setup-current-password"
                type="password"
                autoComplete="current-password"
                autoFocus
                value={currentPassword}
                onChange={(e) => {
                  setCurrentPassword(e.target.value);
                }}
              />
            </div>
          )}
          {setupError && (
            <p className="text-sm text-destructive">{setupError}</p>
          )}
          <DialogFooter>
            <Button variant="ghost" onClick={closeSetupDialog}>
              Cancel
            </Button>
            <Button
              disabled={
                hasPassword === undefined ||
                (hasPassword && !currentPassword) ||
                setupMutation.isPending
              }
              onClick={() => void handleStartSetup()}
            >
              {setupMutation.isPending && (
                <Loader2 className="mr-2 h-4 w-4 animate-spin" />
              )}
              Continue
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Disable 2FA dialog */}
      <Dialog open={disableDialogOpen} onOpenChange={setDisableDialogOpen}>
        <DialogContent fallbackFocus={() => twoFactorCard.current}>
          <DialogHeader>
            <DialogTitle>Disable two-factor authentication</DialogTitle>
            <DialogDescription>
              Enter your current TOTP code to disable 2FA. This will remove the
              extra security layer from your account.
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-2">
            <Label htmlFor="disable-code">Authentication code</Label>
            <Input
              id="disable-code"
              type="text"
              inputMode="numeric"
              pattern="[0-9]*"
              maxLength={6}
              placeholder="000000"
              autoComplete="one-time-code"
              autoFocus
              value={disableCode}
              onChange={(e) => {
                setDisableCode(e.target.value.replace(/\D/g, ""));
              }}
            />
          </div>
          <DialogFooter>
            <Button
              variant="ghost"
              onClick={() => {
                setDisableDialogOpen(false);
              }}
            >
              Cancel
            </Button>
            <Button
              variant="destructive"
              disabled={disableCode.length !== 6 || disableMutation.isPending}
              onClick={() => void handleDisable()}
            >
              {disableMutation.isPending && (
                <Loader2 className="mr-2 h-4 w-4 animate-spin" />
              )}
              Disable 2FA
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Regenerate recovery codes dialog */}
      <Dialog open={regenDialogOpen} onOpenChange={setRegenDialogOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Regenerate recovery codes</DialogTitle>
            <DialogDescription>
              This will invalidate all existing recovery codes and generate new
              ones. Enter your current TOTP code to continue.
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-2">
            <Label htmlFor="regen-code">Authentication code</Label>
            <Input
              id="regen-code"
              type="text"
              inputMode="numeric"
              pattern="[0-9]*"
              maxLength={6}
              placeholder="000000"
              autoComplete="one-time-code"
              autoFocus
              value={regenCode}
              onChange={(e) => {
                setRegenCode(e.target.value.replace(/\D/g, ""));
              }}
            />
          </div>
          <DialogFooter>
            <Button
              variant="ghost"
              onClick={() => {
                setRegenDialogOpen(false);
              }}
            >
              Cancel
            </Button>
            <Button
              disabled={regenCode.length !== 6 || regenMutation.isPending}
              onClick={() => void handleRegenerate()}
            >
              {regenMutation.isPending && (
                <Loader2 className="mr-2 h-4 w-4 animate-spin" />
              )}
              Regenerate
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
