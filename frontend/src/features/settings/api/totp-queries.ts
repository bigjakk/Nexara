import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";
import type {
  TOTPSetupResponse,
  TOTPConfirmResponse,
  TOTPStatusResponse,
} from "@/types/api";

export function useTOTPStatus() {
  return useQuery({
    queryKey: ["totp", "status"],
    queryFn: () =>
      apiClient.get<TOTPStatusResponse>("/api/v1/auth/totp/status"),
  });
}

export function useTOTPSetup() {
  return useMutation({
    mutationFn: () =>
      apiClient.post<TOTPSetupResponse>("/api/v1/auth/totp/setup"),
  });
}

export function useTOTPConfirm() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (code: string) =>
      apiClient.post<TOTPConfirmResponse>("/api/v1/auth/totp/setup/verify", {
        code,
      }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["totp", "status"] });
    },
  });
}

export function useTOTPDisable() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: { code?: string; recovery_code?: string }) =>
      apiClient.delete("/api/v1/auth/totp", data),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["totp", "status"] });
    },
  });
}

export function useRegenerateRecoveryCodes() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (code: string) =>
      apiClient.post<{ recovery_codes: string[] }>(
        "/api/v1/auth/totp/recovery-codes/regenerate",
        { code },
      ),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["totp", "status"] });
    },
  });
}

export function useAdminResetTOTP() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (userId: string) =>
      apiClient.delete(`/api/v1/users/${userId}/totp`),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["admin", "users"] });
    },
  });
}
