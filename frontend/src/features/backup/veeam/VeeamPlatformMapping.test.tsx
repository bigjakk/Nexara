import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { apiClient } from "@/lib/api-client";
import { VeeamPlatformMapping } from "./VeeamPlatformMapping";
import type { VeeamPlatform, VeeamInfrastructureGuest } from "../types/backup";

vi.mock("@/lib/api-client", () => ({
  apiClient: { list: vi.fn(), put: vi.fn() },
}));

const mockedList = vi.mocked(apiClient.list);

const C02 = "c0000000-0000-4000-8000-000000000001";

function platform(over: Partial<VeeamPlatform> = {}): VeeamPlatform {
  return {
    platform_id: "01208ee8-47fe-4ea8-8727-5115874da1ad",
    display_name: "cluster01",
    cluster_id: null,
    cluster_name: "",
    object_count: 18,
    last_seen_at: "2026-08-27T02:00:00Z",
    ...over,
  };
}

function guest(
  over: Partial<VeeamInfrastructureGuest> = {},
): VeeamInfrastructureGuest {
  return {
    id: "infra-1",
    veeam_ref: "33de6836-c00c-4d7c-bd37-2bdf37743b47",
    role: "worker",
    name: "vbr01-worker01",
    host_name: "pve-01.example.com",
    is_disabled: false,
    is_online: false,
    cluster_id: C02,
    cluster_name: "cluster02",
    vmid: 103,
    guest_name: "vbr01-worker01",
    last_seen_at: "2026-08-27T02:00:00Z",
    ...over,
  };
}

describe("VeeamPlatformMapping", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockedList.mockResolvedValue([{ id: C02, name: "cluster02" }]);
  });

  it("warns that an unmapped connection is invisible, not merely untidy", () => {
    renderWithProviders(
      <VeeamPlatformMapping
        serverId="srv-1"
        platforms={[platform()]}
        infrastructure={[]}
      />,
    );
    // The consequence, not the state. An operator has no other way to learn
    // that these guests are missing from coverage entirely.
    expect(
      screen.getByText(/missing from backup coverage/i),
    ).toBeInTheDocument();
  });

  it("says nothing when every connection is mapped", () => {
    renderWithProviders(
      <VeeamPlatformMapping
        serverId="srv-1"
        platforms={[platform({ cluster_id: C02, cluster_name: "cluster02" })]}
        infrastructure={[]}
      />,
    );
    expect(
      screen.queryByText(/missing from backup coverage/i),
    ).not.toBeInTheDocument();
    expect(screen.getByText("Mapped")).toBeInTheDocument();
  });

  it("names the mapped cluster even when it is absent from the caller's cluster list", async () => {
    // Changing a mapping needs only GLOBAL manage:veeam, while /clusters is
    // filtered by per-cluster view:cluster — so a legitimate operator can be
    // mapped to a cluster they cannot list. Falling back to the "Not mapped"
    // placeholder beside a "Mapped" badge would be the one claim this control
    // must never get wrong.
    mockedList.mockResolvedValue([]);
    renderWithProviders(
      <VeeamPlatformMapping
        serverId="srv-1"
        platforms={[platform({ cluster_id: C02, cluster_name: "cluster02" })]}
        infrastructure={[]}
      />,
    );
    expect(await screen.findByText("cluster02")).toBeInTheDocument();
  });

  it("lists the guests coverage excludes, so the exclusion can be inspected", () => {
    renderWithProviders(
      <VeeamPlatformMapping
        serverId="srv-1"
        platforms={[platform({ cluster_id: C02, cluster_name: "cluster02" })]}
        infrastructure={[guest()]}
      />,
    );
    // An exclusion nobody can see is indistinguishable from a coverage bug.
    // Twice by design: the name Veeam knows it by, and the guest it matched.
    expect(screen.getAllByText(/vbr01-worker01/)).toHaveLength(2);
    expect(screen.getByText("Worker")).toBeInTheDocument();
    expect(screen.getByText("(cluster02 #103)")).toBeInTheDocument();
  });

  it("flags a Veeam-owned machine that matched no guest", () => {
    renderWithProviders(
      <VeeamPlatformMapping
        serverId="srv-1"
        platforms={[platform({ cluster_id: C02, cluster_name: "cluster02" })]}
        infrastructure={[
          guest({ vmid: null, cluster_id: null, cluster_name: "" }),
        ]}
      />,
    );
    // It is still being counted as an ordinary unprotected VM, which is the
    // noise this whole eligibility model exists to remove.
    expect(
      screen.getByText(/no guest on a mapped cluster/i),
    ).toBeInTheDocument();
  });
});
