import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";
import { apiPath } from "@/lib/api-path";

export interface ProfileResponse {
  id: string;
  email: string;
  display_name: string;
  role: string;
  auth_source: "local" | "ldap" | "oidc";
  totp_enabled: boolean;
  created_at: string;
}

interface UpdateProfileParams {
  display_name: string;
}

interface ChangePasswordParams {
  old_password: string;
  new_password: string;
}

export function useProfile() {
  return useQuery({
    queryKey: ["auth", "me"],
    queryFn: () => apiClient.get<ProfileResponse>(apiPath`/api/v1/auth/me`),
  });
}

/**
 * Opts a mutation out of the app's global error toast: the profile page shows
 * the failure itself, and toasts one that settles after it is gone
 * (useSaveOutcome). Anything that relies on the toast must not use it.
 */
const errorsHandledLocally = { onError: () => undefined };

export function useUpdateProfile() {
  const queryClient = useQueryClient();
  return useMutation({
    ...errorsHandledLocally,
    mutationFn: (params: UpdateProfileParams) =>
      apiClient.put<ProfileResponse>(apiPath`/api/v1/auth/profile`, params),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["auth", "me"] });
    },
  });
}

export function useChangePassword() {
  return useMutation({
    ...errorsHandledLocally,
    mutationFn: (params: ChangePasswordParams) =>
      apiClient.post<{ message: string }>(
        apiPath`/api/v1/auth/change-password`,
        params,
      ),
    // Its variables hold both passwords: gcTime 0 drops the finished mutation
    // as soon as nothing observes it, and the dialog resets it when it closes
    // (ProfilePage), so the passwords go when the dialog does.
    gcTime: 0,
  });
}
