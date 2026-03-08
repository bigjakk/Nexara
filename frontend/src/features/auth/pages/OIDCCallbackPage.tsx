import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate, useSearchParams } from "react-router-dom";
import { Loader2, ShieldCheck, XCircle } from "lucide-react";
import { apiClient, ApiClientError, storeTokens } from "@/lib/api-client";
import { useAuthStore } from "@/stores/auth-store";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
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
        "/api/v1/auth/oidc/token-exchange",
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
        "/api/v1/auth/totp/verify-login",
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
      <div className="flex min-h-screen items-center justify-center px-4">
        <div className="w-full max-w-md space-y-4 text-center">
          <XCircle className="mx-auto h-12 w-12 text-destructive" />
          <h1 className="text-xl font-semibold">{t("ssoAuthenticationFailed")}</h1>
          <p className="text-muted-foreground">{error}</p>
          <a
            href="/login"
            className="inline-block text-primary underline-offset-4 hover:underline"
          >
            {t("backToLogin")}
          </a>
        </div>
      </div>
    );
  }

  if (totpPending) {
    return (
      <div className="flex min-h-screen items-center justify-center px-4">
        <Card className="w-full max-w-md">
          <CardHeader className="text-center">
            <div className="mx-auto mb-4 flex h-12 w-12 items-center justify-center rounded-lg bg-primary">
              <ShieldCheck className="h-6 w-6 text-primary-foreground" />
            </div>
            <CardTitle className="text-2xl">
              {t("twoFactorAuthentication")}
            </CardTitle>
            <CardDescription>
              {useRecoveryCode
                ? t("enterRecoveryCodes")
                : t("enterSixDigitCode")}
            </CardDescription>
          </CardHeader>
          <form onSubmit={(e) => void handleTotpSubmit(e)}>
            <CardContent className="space-y-4">
              {error && (
                <div className="rounded-md bg-destructive/10 p-3 text-sm text-destructive">
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
            </CardContent>
            <CardFooter className="flex flex-col gap-3">
              <Button
                type="submit"
                className="w-full"
                disabled={
                  verifying ||
                  (useRecoveryCode ? !recoveryCode : totpCode.length !== 6)
                }
              >
                {verifying && (
                  <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                )}
                {t("verify")}
              </Button>
              <Button
                type="button"
                variant="ghost"
                className="w-full text-sm"
                onClick={() => {
                  setUseRecoveryCode(!useRecoveryCode);
                  setError(null);
                }}
              >
                {useRecoveryCode
                  ? t("useAuthenticatorApp")
                  : t("useRecoveryCode")}
              </Button>
            </CardFooter>
          </form>
        </Card>
      </div>
    );
  }

  return (
    <div className="flex min-h-screen items-center justify-center">
      <div className="space-y-4 text-center">
        <Loader2 className="mx-auto h-8 w-8 animate-spin text-muted-foreground" />
        <p className="text-muted-foreground">{t("completingSSOSignIn")}</p>
      </div>
    </div>
  );
}
