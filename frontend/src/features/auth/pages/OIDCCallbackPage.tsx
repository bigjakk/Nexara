import { useEffect, useState } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { Loader2, XCircle } from "lucide-react";
import { apiClient } from "@/lib/api-client";
import { storeTokens } from "@/lib/api-client";
import { useAuthStore } from "@/stores/auth-store";
import type { AuthResponse } from "@/types/api";

export function OIDCCallbackPage() {
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();
  const [error, setError] = useState<string | null>(null);
  const setAuth = useAuthStore((s) => s.setAuthFromResponse);

  useEffect(() => {
    const oidcToken = searchParams.get("oidc_token");
    const errorParam = searchParams.get("error");

    if (errorParam) {
      setError(errorParam);
      return;
    }

    if (!oidcToken) {
      setError("Missing authentication token");
      return;
    }

    let cancelled = false;

    apiClient
      .postPublic<AuthResponse>("/api/v1/auth/oidc/token-exchange", {
        code: oidcToken,
      })
      .then((res) => {
        if (cancelled) return;
        storeTokens(res);
        setAuth(res);
        void navigate("/", { replace: true });
      })
      .catch(() => {
        if (cancelled) return;
        setError("Authentication failed. The SSO token may have expired. Please try again.");
      });

    return () => {
      cancelled = true;
    };
  }, [searchParams, navigate, setAuth]);

  if (error) {
    return (
      <div className="flex min-h-screen items-center justify-center px-4">
        <div className="w-full max-w-md space-y-4 text-center">
          <XCircle className="mx-auto h-12 w-12 text-destructive" />
          <h1 className="text-xl font-semibold">SSO Authentication Failed</h1>
          <p className="text-muted-foreground">{error}</p>
          <a
            href="/login"
            className="inline-block text-primary underline-offset-4 hover:underline"
          >
            Back to login
          </a>
        </div>
      </div>
    );
  }

  return (
    <div className="flex min-h-screen items-center justify-center">
      <div className="space-y-4 text-center">
        <Loader2 className="mx-auto h-8 w-8 animate-spin text-muted-foreground" />
        <p className="text-muted-foreground">Completing SSO sign-in...</p>
      </div>
    </div>
  );
}
