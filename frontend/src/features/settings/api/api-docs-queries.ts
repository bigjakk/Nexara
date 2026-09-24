import { useQuery } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";
import { apiPath } from "@/lib/api-path";
import type { APIEndpoint } from "@/types/api";

export function useAPIDocs() {
  return useQuery({
    queryKey: ["api-docs"],
    queryFn: () => apiClient.list<APIEndpoint>(apiPath`/api/v1/api-docs`),
    staleTime: Infinity,
  });
}
