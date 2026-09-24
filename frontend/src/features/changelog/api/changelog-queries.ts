import { useQuery } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";
import { apiPath } from "@/lib/api-path";
import type { ChangelogEntry } from "@/lib/changelog";

export function useChangelog() {
  return useQuery({
    queryKey: ["changelog"],
    queryFn: () => apiClient.list<ChangelogEntry>(apiPath`/api/v1/changelog`),
    staleTime: 1000 * 60 * 60, // 1 hour — backend caches for the same TTL
  });
}
