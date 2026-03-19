import { useQuery } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";
import type { APIEndpoint } from "@/types/api";

export function useAPIDocs() {
  return useQuery({
    queryKey: ["api-docs"],
    queryFn: () => apiClient.get<APIEndpoint[]>("/api/v1/api-docs"),
    staleTime: Infinity,
  });
}
