import type { RollingUpdateJob } from "@/types/api";

// A pending rolling update job as the API returns it, with overrides.
export function makeRollingUpdateJob(
  overrides: Partial<RollingUpdateJob> = {},
): RollingUpdateJob {
  return {
    id: "j1",
    cluster_id: "c1",
    status: "pending",
    parallelism: 1,
    reboot_after_update: true,
    auto_restore_guests: true,
    package_excludes: [],
    ha_policy: "warn",
    ha_warnings: null,
    auto_upgrade: true,
    drain_guests: true,
    failure_reason: "",
    created_by: "u1",
    created_at: "2026-11-01T10:00:00Z",
    updated_at: "2026-11-01T10:00:00Z",
    ...overrides,
  };
}
