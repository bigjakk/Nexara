import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";
import { apiPath } from "@/lib/api-path";
import { useAuthStore } from "@/stores/auth-store";
import type { Favorite, FavoriteResourceType } from "@/types/api";

/** The identity of a starrable resource — everything the API needs to key it. */
export interface FavoriteTarget {
  resource_type: FavoriteResourceType;
  cluster_id: string;
  /** Empty for a cluster, the node name for a node, the VMID for a guest. */
  ref: string;
}

/**
 * Query key for one user's favorites.
 *
 * Keyed by user id because favorites are personal. Nothing clears the
 * QueryClient on logout — logout is a state change, not a reload — and the
 * client defaults to a 5 minute staleTime, so a shared key would let the next
 * person to sign in on this browser read the previous user's starred resources
 * straight from cache. Those rows carry cluster, node and guest names, so that
 * is a cross-user disclosure and not merely stale UI.
 */
const favoritesKey = (userID: string) => ["favorites", userID] as const;

/**
 * True when two references name the same resource.
 *
 * Compares the whole triple rather than just the ref: a node called "101" and
 * a guest with VMID 101 in the same cluster would otherwise match each other.
 */
export function sameFavorite(a: FavoriteTarget, b: FavoriteTarget): boolean {
  return (
    a.resource_type === b.resource_type &&
    a.cluster_id === b.cluster_id &&
    a.ref === b.ref
  );
}

function fetchFavorites() {
  return apiClient.list<Favorite>(apiPath`/api/v1/favorites`);
}

/** The caller's own starred clusters, nodes and guests, across every cluster. */
export function useFavorites() {
  const userID = useAuthStore((s) => s.user?.id ?? "");
  return useQuery({
    queryKey: favoritesKey(userID),
    queryFn: fetchFavorites,
    // No user means nothing to ask about, and the endpoint would 401 anyway.
    enabled: userID !== "",
  });
}

/**
 * Whether one resource is starred.
 *
 * Reads the same cache entry as useFavorites, so the dozens of context menus on
 * an inventory page share a single request. `select` narrows to a boolean
 * before React sees it: returning a derived array or Set here would hand every
 * caller a new object on each render and re-render the whole table on any
 * unrelated favorites change.
 */
export function useIsFavorite(target: FavoriteTarget | null): boolean {
  const userID = useAuthStore((s) => s.user?.id ?? "");
  const { data } = useQuery({
    queryKey: favoritesKey(userID),
    queryFn: fetchFavorites,
    enabled: userID !== "",
    select: (rows: Favorite[]) =>
      target !== null && rows.some((row) => sameFavorite(row, target)),
  });
  return data ?? false;
}

interface ToggleFavoriteInput {
  target: FavoriteTarget;
  /** The state to move to: true stars, false unstars. */
  favorited: boolean;
}

/**
 * Star or unstar a resource.
 *
 * The invalidation lives HERE, at hook level, rather than being passed per-call
 * from the row that triggered it. That is not a style choice: one useMutation
 * shared across many rows drops the first call's per-call callbacks when a
 * second mutate() runs — mutate() reassigns the observer's options and detaches
 * it from the in-flight mutation. Starring two guests quickly would then skip
 * the refetch for the first, leaving its star drawn hollow until something else
 * happened to invalidate the list.
 *
 * No optimistic write. The list rows carry names, status and OS icons resolved
 * server-side, and inventing them client-side would flash a half-drawn row that
 * changes again a moment later; a star is not worth that.
 */
export function useToggleFavorite() {
  const qc = useQueryClient();
  const userID = useAuthStore((s) => s.user?.id ?? "");

  return useMutation({
    mutationFn: ({ target, favorited }: ToggleFavoriteInput) => {
      if (favorited) {
        return apiClient.post<{ message: string }>(
          apiPath`/api/v1/favorites`,
          target,
        );
      }
      // Query parameters rather than a path, so a node name with a slash or a
      // dot never has to survive URL path segmentation.
      const params = new URLSearchParams({
        resource_type: target.resource_type,
        cluster_id: target.cluster_id,
        ref: target.ref,
      });
      return apiClient.delete<{ message: string }>(
        apiPath`/api/v1/favorites?${params}`,
      );
    },
    // onSettled, not onSuccess: a failed star must also refetch, or the menu
    // keeps showing whatever the click implied rather than what the server has.
    onSettled: () => {
      void qc.invalidateQueries({ queryKey: favoritesKey(userID) });
    },
  });
}
