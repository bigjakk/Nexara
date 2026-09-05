import type { StorageResponse } from "@/types/api";

/**
 * A storage pool row.
 *
 * Shared rather than feature-local: `storage_pools` rows surface in the VM
 * placement filters and in the guest-tools ISO target picker, and those two
 * disagreeing about what a pool looks like is how a filter test passes against
 * a shape the picker never sees.
 *
 * Defaults describe a plain local directory pool. `content` is empty by
 * default so a test that cares about content has to say which content.
 */
export function makeStorage(
  over: Partial<StorageResponse> = {},
): StorageResponse {
  return {
    id: "s1",
    cluster_id: "c1",
    node_id: "n1",
    storage: "test-pool",
    type: "dir",
    content: "",
    active: true,
    enabled: true,
    shared: false,
    total: 0,
    used: 0,
    avail: 0,
    last_seen_at: "",
    created_at: "",
    updated_at: "",
    ...over,
  };
}
