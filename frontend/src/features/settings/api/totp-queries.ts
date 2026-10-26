import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";
import { apiPath } from "@/lib/api-path";
import type {
  TOTPSetupResponse,
  TOTPConfirmResponse,
  TOTPStatusResponse,
} from "@/types/api";

export function useTOTPStatus() {
  return useQuery({
    queryKey: ["totp", "status"],
    queryFn: () =>
      apiClient.get<TOTPStatusResponse>(apiPath`/api/v1/auth/totp/status`),
  });
}

/**
 * Begins the setup, which the server hands out only to the account's owner: an
 * account that signs in with a Nexara password sends the current one, and an
 * LDAP or single sign-on account sends none (it needs a recent sign-in instead).
 */
export function useTOTPSetup() {
  return useMutation({
    mutationFn: (data: { current_password?: string }) =>
      apiClient.post<TOTPSetupResponse>(apiPath`/api/v1/auth/totp/setup`, data),
    // Its variables hold the current password: gcTime 0 drops the finished
    // mutation as soon as nothing observes it, and the dialog resets it when
    // it closes (SecurityPage), so the password goes when the dialog does.
    gcTime: 0,
  });
}

export function useTOTPConfirm() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (code: string) =>
      apiClient.post<TOTPConfirmResponse>(
        apiPath`/api/v1/auth/totp/setup/verify`,
        {
          code,
        },
      ),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["totp", "status"] });
    },
  });
}

export function useTOTPDisable() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: { code?: string; recovery_code?: string }) =>
      apiClient.delete(apiPath`/api/v1/auth/totp`, data),
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
        apiPath`/api/v1/auth/totp/recovery-codes/regenerate`,
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
      apiClient.delete(apiPath`/api/v1/users/${userId}/totp`),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["admin", "users"] });
    },
  });
}
