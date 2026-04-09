import {
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";

import { apiDelete, apiGet, apiPost } from "./api-client";

/**
 * Mobile device registration types and TanStack Query hooks. The backend
 * intentionally hides the raw `expo_push_token` from the response (security
 * fix H1 — push tokens are sender credentials and don't need to leave the
 * server). The mobile UI deletes by `id` instead.
 */

export interface MobileDevice {
  id: string;
  user_id: string;
  device_id: string;
  device_name: string;
  platform: "ios" | "android";
  last_seen_at: string;
  created_at: string;
}

export interface RegisterDeviceRequest {
  device_id: string;
  device_name: string;
  platform: "ios" | "android";
  expo_push_token: string;
}

export function useMyDevices() {
  return useQuery({
    queryKey: ["me", "devices"],
    queryFn: () => apiGet<MobileDevice[]>("/me/devices"),
    staleTime: 30_000,
  });
}

export function useRegisterDevice() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (req: RegisterDeviceRequest) =>
      apiPost<MobileDevice, RegisterDeviceRequest>("/me/devices", req),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["me", "devices"] });
    },
  });
}

export function useDeleteDevice() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      apiDelete<{ message: string }>(`/me/devices/${id}`),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["me", "devices"] });
    },
  });
}
