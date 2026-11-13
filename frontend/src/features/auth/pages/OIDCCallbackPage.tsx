import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate, useSearchParams } from "react-router-dom";
import { Loader2 } from "lucide-react";
import { apiClient, ApiClientError, storeTokens } from "@/lib/api-client";
import { apiPath } from "@/lib/api-path";
import { useAuthStore } from "@/stores/auth-store";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { AuthShell } from "../components/AuthShell";
import type {
  AuthResponse,
  TOTPRequiredResponse,
  TOTPVerifyLoginRequest,
} from "@/types/api";

function isTotpRequired(
  res: AuthResponse | TOTPRequiredResponse,
): res is TOTPRequiredResponse {
  return "totp_pending_token" in res;
}

export function OIDCCallbackPage() {
  const { t } = useTranslation("auth");
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();
  const [error, setError] = useState<string | null>(null);
  const [totpPending, setTotpPending] = useState(false);
  const [totpPendingToken, setTotpPendingToken] = useState("");
  const [totpCode, setTotpCode] = useState("");
  const [recoveryCode, setRecoveryCode] = useState("");
  const [useRecoveryCode, setUseRecoveryCode] = useState(false);
  const [verifying, setVerifying] = useState(false);
  const setAuth = useAuthStore((s) => s.setAuthFromResponse);

  useEffect(() => {
    const oidcToken = searchParams.get("oidc_token");
    const errorParam = searchParams.get("error");

    if (errorParam) {
      setError(errorParam);
      return;
    }

    if (!oidcToken) {
      setError(t("missingAuthToken"));
      return;
    }

    let cancelled = false;

    apiClient
      .postPublic<AuthResponse | TOTPRequiredResponse>(
        apiPath`/api/v1/auth/oidc/token-exchange`,
        { code: oidcToken },
      )
      .then((res) => {
        if (cancelled) return;
        if (isTotpRequired(res)) {
          setTotpPending(true);
          setTotpPendingToken(res.totp_pending_token);
          return;
        }
        storeTokens(res);
        setAuth(res);
        void navigate("/", { replace: true });
      })
      .catch(() => {
        if (cancelled) return;
        setError(t("ssoTokenExpired"));
      });

    return () => {
      cancelled = true;
    };
  }, [searchParams, navigate, setAuth, t]);

  const handleTotpSubmit = async (e: React.SyntheticEvent) => {
    e.preventDefault();
    setError(null);
    setVerifying(true);
    try {
      const body: TOTPVerifyLoginRequest = {
        totp_pending_token: totpPendingToken,
        ...(useRecoveryCode
          ? { recovery_code: recoveryCode }
          : { code: totpCode }),
      };
      const res = await apiClient.postPublic<AuthResponse>(
        apiPath`/api/v1/auth/totp/verify-login`,
        body,
      );
      storeTokens(res);
      setAuth(res);
      void navigate("/", { replace: true });
    } catch (err) {
      if (err instanceof ApiClientError) {
        setError(err.body.message);
      } else {
        setError(t("verificationFailed"));
      }
    } finally {
      setVerifying(false);
    }
  };

  if (error && !totpPending) {
    return (
      <AuthShell title={t("ssoAuthenticationFailed")}>
        <div className="space-y-4">
          <div className="rounded-md bg-destructive/10 p-3 text-sm text-red-700 dark:text-red-400">
            {error}
          </div>
          <Button asChild className="w-full">
            <a href="/login">{t("backToLogin")}</a>
          </Button>
        </div>
      </AuthShell>
    );
  }

  if (totpPending) {
    return (
      <AuthShell
        title={t("twoFactorAuthentication")}
        description={
          useRecoveryCode ? t("enterRecoveryCodes") : t("enterSixDigitCode")
        }
      >
        <form onSubmit={(e) => void handleTotpSubmit(e)} className="space-y-4">
          {error && (
            <div className="rounded-md bg-destructive/10 p-3 text-sm text-red-700 dark:text-red-400">
              {error}
            </div>
          )}
          {useRecoveryCode ? (
            <div className="space-y-2">
              <Label htmlFor="recovery-code">{t("recoveryCode")}</Label>
              <Input
                id="recovery-code"
                type="text"
                placeholder="XXXX-XXXX"
                autoComplete="off"
                autoFocus
                value={recoveryCode}
                onChange={(e) => {
                  setRecoveryCode(e.target.value);
                }}
              />
            </div>
          ) : (
            <div className="space-y-2">
              <Label htmlFor="totp-code">{t("authenticationCode")}</Label>
              <Input
                id="totp-code"
                type="text"
                inputMode="numeric"
                pattern="[0-9]*"
                maxLength={6}
                placeholder="000000"
                autoComplete="one-time-code"
                autoFocus
                value={totpCode}
                onChange={(e) => {
                  setTotpCode(e.target.value.replace(/\D/g, ""));
                }}
              />
            </div>
          )}
          <Button
            type="submit"
            className="w-full"
            disabled={
              verifying ||
              (useRecoveryCode ? !recoveryCode : totpCode.length !== 6)
            }
          >
            {verifying && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
            {t("verify")}
          </Button>
          <div className="flex justify-end">
            <Button
              type="button"
              variant="ghost"
              size="sm"
              className="-mr-3"
              onClick={() => {
                setUseRecoveryCode(!useRecoveryCode);
                setError(null);
              }}
            >
              {useRecoveryCode
                ? t("useAuthenticatorApp")
                : t("useRecoveryCode")}
            </Button>
          </div>
        </form>
      </AuthShell>
    );
  }

  return (
    <AuthShell title={t("completingSSOSignIn")}>
      <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
    </AuthShell>
  );
}
