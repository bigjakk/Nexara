import { useEffect, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { useTranslation } from "react-i18next";
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
import type { SetupStatus } from "@/types/api";

function useRegisterSchema() {
  const { t } = useTranslation("auth");
  return z
    .object({
      email: z.email({ message: t("invalidEmailAddress") }),
      display_name: z.string().min(1, t("displayNameRequired")),
      password: z
        .string()
        .min(8, t("passwordAtLeast8Chars"))
        .max(72, t("passwordAtMost72Chars"))
        .regex(/[A-Z]/, t("mustContainUppercase"))
        .regex(/[a-z]/, t("mustContainLowercase"))
        .regex(/[0-9]/, t("mustContainDigit"))
        .regex(/[^A-Za-z0-9]/, t("mustContainSpecialChar")),
      confirm_password: z.string(),
    })
    .refine((data) => data.password === data.confirm_password, {
      message: t("passwordsDoNotMatch"),
      path: ["confirm_password"],
    });
}

type RegisterFormValues = z.infer<ReturnType<typeof useRegisterSchema>>;

export function RegisterPage() {
  const { t } = useTranslation("auth");
  const { t: tc } = useTranslation("common");
  const navigate = useNavigate();
  const { register: registerUser, isLoading, isAuthenticated } = useAuth();
  const [error, setError] = useState("");
  const [isFirstRun, setIsFirstRun] = useState<boolean | null>(null);

  const registerSchema = useRegisterSchema();

  const {
    register,
    handleSubmit,
    formState: { errors },
  } = useForm<RegisterFormValues>({
    resolver: zodResolver(registerSchema),
  });

  useEffect(() => {
    if (isAuthenticated) {
      void navigate("/", { replace: true });
    }
  }, [isAuthenticated, navigate]);

  useEffect(() => {
    let cancelled = false;
    apiClient
      .getPublic<SetupStatus>("/api/v1/auth/setup-status")
      .then((status) => {
        if (!cancelled) {
          if (!status.needs_setup) {
            // Setup already completed - registration is admin-only via the Users page
            void navigate("/login", { replace: true });
            return;
          }
          setIsFirstRun(status.needs_setup);
        }
      })
      .catch(() => {
        if (!cancelled) setIsFirstRun(null);
      });
    return () => {
      cancelled = true;
    };
  }, [navigate]);

  const onSubmit = async (data: RegisterFormValues) => {
    setError("");
    try {
      await registerUser({
        email: data.email,
        password: data.password,
        display_name: data.display_name,
      });
    } catch (err) {
      if (err instanceof ApiClientError) {
        setError(err.body.message);
      } else {
        setError(tc("unexpectedError"));
      }
    }
  };

  return (
    <div className="flex min-h-screen items-center justify-center px-4">
      <Card className="w-full max-w-md">
        <CardHeader className="text-center">
          <div className="mx-auto mb-4 flex h-12 w-12 items-center justify-center rounded-lg bg-primary">
            <Server className="h-6 w-6 text-primary-foreground" />
          </div>
          <CardTitle className="text-2xl">
            {isFirstRun ? t("setupNexara") : t("createAccount")}
          </CardTitle>
          <CardDescription>
            {isFirstRun
              ? t("createFirstAdminAccount")
              : t("registerNewAccount")}
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
              <Label htmlFor="display_name">{t("displayName")}</Label>
              <Input
                id="display_name"
                placeholder="Admin"
                autoComplete="name"
                {...register("display_name")}
              />
              {errors.display_name && (
                <p className="text-sm text-destructive">
                  {errors.display_name.message}
                </p>
              )}
            </div>
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
                <p className="text-sm text-destructive">
                  {errors.email.message}
                </p>
              )}
            </div>
            <div className="space-y-2">
              <Label htmlFor="password">{t("password")}</Label>
              <Input
                id="password"
                type="password"
                autoComplete="new-password"
                {...register("password")}
              />
              {errors.password && (
                <p className="text-sm text-destructive">
                  {errors.password.message}
                </p>
              )}
              <p className="text-xs text-muted-foreground">
                {t("passwordRequirements")}
              </p>
            </div>
            <div className="space-y-2">
              <Label htmlFor="confirm_password">{t("confirmPassword")}</Label>
              <Input
                id="confirm_password"
                type="password"
                autoComplete="new-password"
                {...register("confirm_password")}
              />
              {errors.confirm_password && (
                <p className="text-sm text-destructive">
                  {errors.confirm_password.message}
                </p>
              )}
            </div>
          </CardContent>
          <CardFooter className="flex flex-col gap-4">
            <Button type="submit" className="w-full" disabled={isLoading}>
              {isLoading && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
              {isFirstRun ? t("createAdminAccount") : t("createAccount")}
            </Button>
            {!isFirstRun && (
              <p className="text-center text-sm text-muted-foreground">
                {t("alreadyHaveAccount")}{" "}
                <Link
                  to="/login"
                  className="font-medium text-primary underline-offset-4 hover:underline"
                >
                  {t("signInLink")}
                </Link>
              </p>
            )}
          </CardFooter>
        </form>
      </Card>
    </div>
  );
}
