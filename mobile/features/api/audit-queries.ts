import { useQuery } from "@tanstack/react-query";

import { apiGet } from "./api-client";
import type { AuditLogEntry } from "./types";

export function useRecentAuditLog() {
  return useQuery({
    queryKey: ["audit-log", "recent"],
    queryFn: () => apiGet<AuditLogEntry[]>("/audit-log/recent"),
    staleTime: 15_000,
  });
}
