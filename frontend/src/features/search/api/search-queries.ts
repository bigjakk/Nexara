import { useQuery } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";
import { apiPath } from "@/lib/api-path";

export interface SearchResult {
  type: string;
  id: string;
  name: string;
  node?: string;
  status?: string;
  cluster_id: string;
  cluster_name: string;
  vmid?: number;
  template?: boolean;
  [key: string]: unknown;
}

export function useGlobalSearch(query: string) {
  return useQuery({
    queryKey: ["search", query],
    queryFn: () =>
      apiClient.list<SearchResult>(apiPath`/api/v1/search?q=${query}`),
    enabled: query.length >= 2,
    staleTime: 10_000,
  });
}
