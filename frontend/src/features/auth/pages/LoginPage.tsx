import { useEffect, useState } from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { useTranslation } from "react-i18next";
import { ArrowLeft, Loader2 } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useAuth } from "@/hooks/useAuth";
import { useAuthStore } from "@/stores/auth-store";
import { apiClient, ApiClientError } from "@/lib/api-client";
import { apiPath } from "@/lib/api-path";
import { sanitizeReturnTo } from "@/lib/return-to";
import type {
  SetupStatus,
  SSOStatus,
  OIDCAuthorizeResponse,
} from "@/types/api";
import { AuthShell } from "../components/AuthShell";
import { InsecureConnectionBanner } from "../components/InsecureConnectionBanner";

function useLoginSchema() {
  const { t } = useTranslation("auth");
  return z.object({
    email: z.email({ message: t("invalidEmailAddress") }),
    password: z.string().min(1, t("passwordRequired")),
  });
}

type LoginFormValues = z.infer<ReturnType<typeof useLoginSchema>>;

export function LoginPage() {
  const { t } = useTranslation("auth");
  const { t: tc } = useTranslation("common");
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();
  const {
    login,
    isLoading,
    isAuthenticated,
    totpPending,
    verifyTotp,
    verifyTotpRecovery,
    clearTotpPending,
  } = useAuth();
  const [error, setError] = useState("");
  // What the sign-out that brought them here left undone (auth-store).
  const signOutNotice = useAuthStore((s) => s.signOutNotice);
  const [checkingSetup, setCheckingSetup] = useState(true);
  const [needsSetup, setNeedsSetup] = useState(false);
  const [ssoStatus, setSSOStatus] = useState<SSOStatus | null>(null);
  const [ssoLoading, setSSOLoading] = useState(false);
  const [totpCode, setTotpCode] = useState("");
  const [useRecoveryCode, setUseRecoveryCode] = useState(false);
  const [recoveryCode, setRecoveryCode] = useState("");

  const loginSchema = useLoginSchema();
  const returnTo = sanitizeReturnTo(searchParams.get("returnTo"));

  const {
    register,
    handleSubmit,
    formState: { errors },
  } = useForm<LoginFormValues>({
    resolver: zodResolver(loginSchema),
  });

  useEffect(() => {
    if (isAuthenticated) {
      void navigate(returnTo, { replace: true });
    }
  }, [isAuthenticated, navigate, returnTo]);

  useEffect(() => {
    let cancelled = false;
    apiClient
      .getPublic<SetupStatus>(apiPath`/api/v1/auth/setup-status`)
      .then((status) => {
        if (!cancelled) {
          setNeedsSetup(status.needs_setup);
          if (status.needs_setup) {
            void navigate("/register", { replace: true });
          }
        }
      })
      .catch(() => {
        // Backend unreachable — stay on login page
      })
      .finally(() => {
        if (!cancelled) setCheckingSetup(false);
      });
    return () => {
      cancelled = true;
    };
  }, [navigate]);

  useEffect(() => {
    let cancelled = false;
    apiClient
      .getPublic<SSOStatus>(apiPath`/api/v1/auth/sso-status`)
      .then((status) => {
        if (!cancelled) setSSOStatus(status);
      })
      .catch(() => {
        // SSO check failed — no SSO button shown
      });
    return () => {
      cancelled = true;
    };
  }, []);

  // Check for SSO error in URL params
  useEffect(() => {
    const errorParam = searchParams.get("error");
    if (errorParam) {
      setError(errorParam);
    }
  }, [searchParams]);

  const handleSSOLogin = async () => {
    setSSOLoading(true);
    setError("");
    try {
      const res = await apiClient.getPublic<OIDCAuthorizeResponse>(
        apiPath`/api/v1/auth/oidc/authorize`,
      );
      window.location.href = res.redirect_url;
    } catch {
      setError(t("failedToInitiateSSOLogin"));
      setSSOLoading(false);
    }
  };

  const onSubmit = async (data: LoginFormValues) => {
    setError("");
    try {
      await login(data);
    } catch (err) {
      if (err instanceof ApiClientError) {
        setError(err.body.message);
      } else {
        setError(tc("unexpectedError"));
      }
    }
  };

  const handleTotpSubmit = async (e: React.SyntheticEvent) => {
    e.preventDefault();
    setError("");
    try {
      if (useRecoveryCode) {
        await verifyTotpRecovery(recoveryCode);
      } else {
        await verifyTotp(totpCode);
      }
    } catch (err) {
      if (err instanceof ApiClientError) {
        setError(err.body.message);
      } else {
        setError(tc("unexpectedError"));
      }
    }
  };

  const handleBackToLogin = () => {
    clearTotpPending();
    setTotpCode("");
    setRecoveryCode("");
    setUseRecoveryCode(false);
    setError("");
  };

  if (checkingSetup) {
    return (
      <div className="flex h-screen items-center justify-center">
        <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" />
      </div>
    );
  }

  // TOTP verification step
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
              isLoading ||
              (useRecoveryCode ? !recoveryCode : totpCode.length !== 6)
            }
          >
            {isLoading && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
            {t("verify")}
          </Button>
          <div className="flex flex-wrap items-center justify-between gap-x-2 gap-y-1">
            <Button
              type="button"
              variant="ghost"
              size="sm"
              className="-ml-3 text-muted-foreground"
              onClick={handleBackToLogin}
            >
              <ArrowLeft className="h-3 w-3" />
              {t("backToLogin")}
            </Button>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              className="-mr-3"
              onClick={() => {
                setUseRecoveryCode(!useRecoveryCode);
                setError("");
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
    <AuthShell
      banner={<InsecureConnectionBanner />}
      title={t("welcomeToNexara")}
      description={t("signInToManage")}
    >
      <form
        onSubmit={(e) => void handleSubmit(onSubmit)(e)}
        className="space-y-4"
      >
        {signOutNotice && (
          <div role="status" className="rounded-md border p-3 text-sm">
            {t(signOutNotice)}
          </div>
        )}
        {error && (
          <div className="rounded-md bg-destructive/10 p-3 text-sm text-red-700 dark:text-red-400">
            {error}
          </div>
        )}
        <div className="space-y-2">
          <Label htmlFor="email">{t("email")}</Label>
          <Input
            id="email"
            type="email"
            placeholder="admin@example.com"
            autoComplete="email"
            {...register("email")}
          />
          {errors.email && (
            <p className="text-sm text-destructive">{errors.email.message}</p>
          )}
        </div>
        <div className="space-y-2">
          <Label htmlFor="password">{t("password")}</Label>
          <Input
            id="password"
            type="password"
            autoComplete="current-password"
            {...register("password")}
          />
          {errors.password && (
            <p className="text-sm text-destructive">
              {errors.password.message}
            </p>
          )}
        </div>
        <Button type="submit" className="w-full" disabled={isLoading}>
          {isLoading && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
          {t("signIn")}
        </Button>
        {ssoStatus?.oidc_enabled && (
          <>
            <div className="flex w-full items-center gap-3">
              <div className="h-px flex-1 bg-border" />
              <span className="text-xs text-muted-foreground">{t("or")}</span>
              <div className="h-px flex-1 bg-border" />
            </div>
            <Button
              type="button"
              variant="outline"
              className="w-full"
              disabled={ssoLoading}
              onClick={() => void handleSSOLogin()}
            >
              {ssoLoading && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
              {t("signInWith", {
                provider: ssoStatus.oidc_provider_name || "SSO",
              })}
            </Button>
          </>
        )}
        {needsSetup && (
          <p className="text-sm text-muted-foreground">
            {t("firstTime")}{" "}
            <Link
              to="/register"
              className="font-medium text-primary underline-offset-4 hover:underline"
            >
              {t("createAdminAccount")}
            </Link>
          </p>
        )}
      </form>
    </AuthShell>
  );
}
