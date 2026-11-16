import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";
import { apiPath } from "@/lib/api-path";
import { errorsHandledLocally } from "@/hooks/useSaveOutcome";
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
 * account that signs in with a Nexara password sends the current one, an LDAP
 * account its directory password, and a single sign-on account none (it has
 * confirmed with its identity provider first: useTOTPSSOStepUp). Its only
 * caller, SecurityPage, shows a failure in its dialog and settles it through
 * useSaveOutcome, so the global toast stays out of it.
 */
export function useTOTPSetup() {
  return useMutation({
    mutationFn: (data: { current_password?: string }) =>
      apiClient.post<TOTPSetupResponse>(apiPath`/api/v1/auth/totp/setup`, data),
    // Its variables hold the current password: gcTime 0 drops the finished
    // mutation as soon as nothing observes it, and the dialog resets it when
    // it closes (SecurityPage), so the password goes when the dialog does.
    gcTime: 0,
    ...errorsHandledLocally,
  });
}

/**
 * Starts a single sign-on account's confirmation at its identity provider,
 * which the setup needs: the answer is the URL to send the browser to, and the
 * provider sends it back to the Security page. Settled like useTOTPSetup.
 */
export function useTOTPSSOStepUp() {
  return useMutation({
    mutationFn: () =>
      apiClient.post<{ redirect_url: string }>(
        apiPath`/api/v1/auth/totp/setup/sso-step-up`,
        {},
      ),
    ...errorsHandledLocally,
  });
}

/** Confirm, Disable and Regenerate: SecurityPage, their only caller, shows a
 * failure itself and settles it through useSaveOutcome. */
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
    ...errorsHandledLocally,
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
    ...errorsHandledLocally,
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
    ...errorsHandledLocally,
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
