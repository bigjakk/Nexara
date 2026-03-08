import { useEffect, useState } from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { Loader2, Server } from "lucide-react";

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
import { useAuth } from "@/hooks/useAuth";
import { apiClient, ApiClientError } from "@/lib/api-client";
import type { SetupStatus, SSOStatus, OIDCAuthorizeResponse } from "@/types/api";

function sanitizeReturnTo(value: string | null): string {
  if (!value || !value.startsWith("/") || value.startsWith("//")) return "/";
  return value;
}

const loginSchema = z.object({
  email: z.email({ message: "Invalid email address" }),
  password: z.string().min(1, "Password is required"),
});

type LoginFormValues = z.infer<typeof loginSchema>;

export function LoginPage() {
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();
  const { login, isLoading, isAuthenticated } = useAuth();
  const [error, setError] = useState("");
  const [checkingSetup, setCheckingSetup] = useState(true);
  const [ssoStatus, setSSOStatus] = useState<SSOStatus | null>(null);
  const [ssoLoading, setSSOLoading] = useState(false);

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
      .getPublic<SetupStatus>("/api/v1/auth/setup-status")
      .then((status) => {
        if (!cancelled && status.needs_setup) {
          void navigate("/register", { replace: true });
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
      .getPublic<SSOStatus>("/api/v1/auth/sso-status")
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
        "/api/v1/auth/oidc/authorize",
      );
      window.location.href = res.redirect_url;
    } catch {
      setError("Failed to initiate SSO login");
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
        setError("An unexpected error occurred");
      }
    }
  };

  if (checkingSetup) {
    return (
      <div className="flex h-screen items-center justify-center">
        <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" />
      </div>
    );
  }

  return (
    <div className="flex min-h-screen items-center justify-center px-4">
      <Card className="w-full max-w-md">
        <CardHeader className="text-center">
          <div className="mx-auto mb-4 flex h-12 w-12 items-center justify-center rounded-lg bg-primary">
            <Server className="h-6 w-6 text-primary-foreground" />
          </div>
          <CardTitle className="text-2xl">Welcome to ProxDash</CardTitle>
          <CardDescription>
            Sign in to manage your Proxmox infrastructure
          </CardDescription>
        </CardHeader>
        <form onSubmit={(e) => void handleSubmit(onSubmit)(e)}>
          <CardContent className="space-y-4">
            {error && (
              <div className="rounded-md bg-destructive/10 p-3 text-sm text-destructive">
                {error}
              </div>
            )}
            <div className="space-y-2">
              <Label htmlFor="email">Email</Label>
              <Input
                id="email"
                type="email"
                placeholder="admin@example.com"
                autoComplete="email"
                {...register("email")}
              />
              {errors.email && (
                <p className="text-sm text-destructive">
                  {errors.email.message}
                </p>
              )}
            </div>
            <div className="space-y-2">
              <Label htmlFor="password">Password</Label>
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
          </CardContent>
          <CardFooter className="flex flex-col gap-4">
            <Button type="submit" className="w-full" disabled={isLoading}>
              {isLoading && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
              Sign In
            </Button>
            {ssoStatus?.oidc_enabled && (
              <>
                <div className="flex w-full items-center gap-3">
                  <div className="h-px flex-1 bg-border" />
                  <span className="text-xs text-muted-foreground">or</span>
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
                  Sign in with {ssoStatus.oidc_provider_name || "SSO"}
                </Button>
              </>
            )}
            <p className="text-center text-sm text-muted-foreground">
              First time?{" "}
              <Link
                to="/register"
                className="font-medium text-primary underline-offset-4 hover:underline"
              >
                Create admin account
              </Link>
            </p>
          </CardFooter>
        </form>
      </Card>
    </div>
  );
}
