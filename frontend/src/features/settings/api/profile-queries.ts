import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";

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
    queryFn: () => apiClient.get<ProfileResponse>("/api/v1/auth/me"),
  });
}

export function useUpdateProfile() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (params: UpdateProfileParams) =>
      apiClient.put<ProfileResponse>("/api/v1/auth/profile", params),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["auth", "me"] });
    },
  });
}

export function useChangePassword() {
  return useMutation({
    mutationFn: (params: ChangePasswordParams) =>
      apiClient.post<{ message: string }>("/api/v1/auth/change-password", params),
  });
}
