import { useQuery } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";
import type { ChangelogEntry } from "@/lib/changelog";

interface ChangelogResponse {
  entries: ChangelogEntry[];
}

export function useChangelog() {
  return useQuery({
    queryKey: ["changelog"],
    queryFn: async () => {
      const data = await apiClient.get<ChangelogResponse>("/api/v1/changelog");
      return data.entries;
    },
    staleTime: 1000 * 60 * 60, // 1 hour — backend caches for the same TTL
    retry: 1,
  });
}
