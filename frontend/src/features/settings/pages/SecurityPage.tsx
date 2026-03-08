import { useState } from "react";
import { QRCodeSVG } from "qrcode.react";
import {
  Copy,
  Download,
  Loader2,
  ShieldCheck,
  ShieldOff,
  RefreshCw,
} from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
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
import { ApiClientError } from "@/lib/api-client";
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

  const [disableDialogOpen, setDisableDialogOpen] = useState(false);
  const [disableCode, setDisableCode] = useState("");

  const [regenDialogOpen, setRegenDialogOpen] = useState(false);
  const [regenCode, setRegenCode] = useState("");

  const handleStartSetup = async () => {
    setError("");
    try {
      const res = await setupMutation.mutateAsync();
      setOtpauthUrl(res.otpauth_url);
      setManualSecret(res.secret);
      setSetupStep("qr");
    } catch (err) {
      if (err instanceof ApiClientError) {
        setError(err.body.message);
      } else {
        setError("Failed to start setup");
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
        "ProxDash Recovery Codes\n",
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
    a.download = "proxdash-recovery-codes.txt";
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

  return (
    <div className="mx-auto max-w-2xl space-y-6 p-6">
      <div>
        <h1 className="text-2xl font-bold">Security Settings</h1>
        <p className="text-muted-foreground">
          Manage two-factor authentication for your account
        </p>
      </div>

      {error && (
        <div className="rounded-md bg-destructive/10 p-3 text-sm text-destructive">
          {error}
        </div>
      )}

      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <ShieldCheck className="h-5 w-5" />
            Two-Factor Authentication (2FA)
          </CardTitle>
          <CardDescription>
            Add an extra layer of security with a TOTP authenticator app
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          {status?.enabled ? (
            <div className="space-y-4">
              <div className="flex items-center justify-between rounded-lg border p-4">
                <div>
                  <p className="font-medium text-green-600">2FA is enabled</p>
                  <p className="text-sm text-muted-foreground">
                    {status.recovery_codes_remaining} recovery code
                    {status.recovery_codes_remaining !== 1 ? "s" : ""} remaining
                  </p>
                </div>
                <ShieldCheck className="h-8 w-8 text-green-600" />
              </div>

              {status.recovery_codes_remaining < 3 && (
                <div className="rounded-md bg-amber-50 border border-amber-200 p-3 text-sm text-amber-800 dark:bg-amber-950/20 dark:border-amber-800 dark:text-amber-200">
                  You have few recovery codes left. Consider regenerating them.
                </div>
              )}

              <div className="flex gap-2">
                <Button
                  variant="outline"
                  onClick={() => {
                    setRegenDialogOpen(true);
                    setError("");
                  }}
                >
                  <RefreshCw className="mr-2 h-4 w-4" />
                  Regenerate Recovery Codes
                </Button>
                <Button
                  variant="destructive"
                  onClick={() => {
                    setDisableDialogOpen(true);
                    setError("");
                  }}
                >
                  <ShieldOff className="mr-2 h-4 w-4" />
                  Disable 2FA
                </Button>
              </div>
            </div>
          ) : setupStep === "idle" ? (
            <div className="space-y-4">
              <div className="flex items-center justify-between rounded-lg border border-dashed p-4">
                <div>
                  <p className="font-medium">2FA is not enabled</p>
                  <p className="text-sm text-muted-foreground">
                    Protect your account with a TOTP authenticator
                  </p>
                </div>
                <ShieldOff className="h-8 w-8 text-muted-foreground" />
              </div>
              <Button onClick={() => void handleStartSetup()}>
                {setupMutation.isPending && (
                  <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                )}
                Enable 2FA
              </Button>
            </div>
          ) : setupStep === "qr" ? (
            <div className="space-y-6">
              <div>
                <h3 className="mb-2 font-medium">
                  Step 1: Scan the QR code with your authenticator app
                </h3>
                <div className="flex justify-center rounded-lg border bg-white p-6">
                  <QRCodeSVG value={otpauthUrl} size={200} />
                </div>
              </div>
              <div>
                <p className="mb-1 text-sm text-muted-foreground">
                  Or enter this secret manually:
                </p>
                <code className="block rounded bg-muted px-3 py-2 text-sm font-mono break-all select-all">
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
          ) : setupStep === "verify" ? (
            <div className="space-y-4">
              <h3 className="font-medium">
                Step 2: Enter the 6-digit code from your authenticator
              </h3>
              <div className="max-w-xs space-y-2">
                <Label htmlFor="setup-verify-code">Verification Code</Label>
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
                  Verify & Enable
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
        </CardContent>
      </Card>

      {/* Recovery Codes Display */}
      {setupStep === "recovery" && recoveryCodes.length > 0 && (
        <Card className="border-green-200 dark:border-green-800">
          <CardHeader>
            <CardTitle>Recovery Codes</CardTitle>
            <CardDescription>
              Save these codes in a safe place. Each code can only be used once
              to sign in if you lose access to your authenticator app.
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="grid grid-cols-2 gap-2 rounded-lg bg-muted p-4 font-mono text-sm">
              {recoveryCodes.map((code) => (
                <div key={code} className="py-1">
                  {code}
                </div>
              ))}
            </div>
            <div className="flex gap-2">
              <Button variant="outline" size="sm" onClick={copyRecoveryCodes}>
                <Copy className="mr-2 h-4 w-4" />
                Copy
              </Button>
              <Button
                variant="outline"
                size="sm"
                onClick={downloadRecoveryCodes}
              >
                <Download className="mr-2 h-4 w-4" />
                Download
              </Button>
            </div>
            <Button
              onClick={() => {
                setSetupStep("idle");
                setRecoveryCodes([]);
              }}
            >
              Done
            </Button>
          </CardContent>
        </Card>
      )}

      {/* Disable 2FA Dialog */}
      <Dialog open={disableDialogOpen} onOpenChange={setDisableDialogOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Disable Two-Factor Authentication</DialogTitle>
            <DialogDescription>
              Enter your current TOTP code to disable 2FA. This will remove the
              extra security layer from your account.
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-2">
            <Label htmlFor="disable-code">Authentication Code</Label>
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

      {/* Regenerate Recovery Codes Dialog */}
      <Dialog open={regenDialogOpen} onOpenChange={setRegenDialogOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Regenerate Recovery Codes</DialogTitle>
            <DialogDescription>
              This will invalidate all existing recovery codes and generate new
              ones. Enter your current TOTP code to continue.
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-2">
            <Label htmlFor="regen-code">Authentication Code</Label>
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
