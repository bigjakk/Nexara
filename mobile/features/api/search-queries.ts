/**
 * Global search query hook.
 *
 * Mirrors the web frontend's `useGlobalSearch` and hits the same backend
 * endpoint at `GET /api/v1/search?q=...` (handler in
 * `internal/api/handlers/search.go`). Searches across VMs, containers,
 * nodes, storage pools, and clusters; returns up to 100 results.
 *
 * The backend ignores queries shorter than 2 characters and returns an
 * empty array. We mirror that on the client by disabling the query
 * entirely below the threshold so we don't even fire the network request.
 *
 * Debouncing is the caller's responsibility — the SearchModal debounces
 * the input value at 250ms before passing it here.
 *
 * RBAC: requires `view:cluster`. Surface the search button conditionally
 * via `usePermissions().canView("cluster")` so we don't show an icon
 * that produces a 403 on tap.
 */

import { useQuery } from "@tanstack/react-query";

import { apiGet } from "./api-client";
import { queryKeys } from "./query-keys";
import type { SearchResult } from "./types";

const MIN_QUERY_LENGTH = 2;

export function useGlobalSearch(query: string) {
  const trimmed = query.trim();
  const enabled = trimmed.length >= MIN_QUERY_LENGTH;

  return useQuery({
    queryKey: queryKeys.search(trimmed),
    queryFn: () =>
      apiGet<SearchResult[]>(`/search?q=${encodeURIComponent(trimmed)}`),
    enabled,
    // Search results are cheap to refetch and the user is actively typing —
    // staleness doesn't matter much. Keep them in the cache briefly so
    // backspacing+retyping the same query is instant.
    staleTime: 30_000,
  });
}
