import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/test-utils";
import { VirtioWinConfigCard } from "./VirtioWinConfigCard";
import {
  useVirtioWinConfig,
  useUpdateVirtioWinConfig,
  useVirtioWinReleases,
  useDownloadVirtioWin,
} from "../api/virtio-win-queries";
import { useClusterStorage } from "@/features/storage/api/storage-queries";
import { usePermissions } from "@/hooks/usePermissions";
import type {
  VirtioWinConfig,
  VirtioWinConfigRequest,
  VirtioWinRelease,
} from "../types/virtio-win";

/** The request body of the first mutate() call, typed rather than indexed. */
function firstCallBody(
  mutate: ReturnType<typeof vi.fn>,
): VirtioWinConfigRequest {
  const call = mutate.mock.calls[0];
  expect(call).toBeDefined();
  return (call as unknown[])[0] as VirtioWinConfigRequest;
}

vi.mock("../api/virtio-win-queries", () => ({
  useVirtioWinConfig: vi.fn(),
  useUpdateVirtioWinConfig: vi.fn(),
  useVirtioWinReleases: vi.fn(),
  useDownloadVirtioWin: vi.fn(),
}));
vi.mock("@/features/storage/api/storage-queries", () => ({
  useClusterStorage: vi.fn(),
}));
vi.mock("@/hooks/usePermissions", () => ({
  usePermissions: vi.fn(),
}));

const baseConfig: VirtioWinConfig = {
  cluster_id: "c1",
  enabled: true,
  storage: "local",
  node: "",
  target_version: "",
  prune_enabled: false,
  last_check_at: null,
  last_error: "",
  effective_version: "0.1.302-1",
};

const releases: VirtioWinRelease[] = [
  {
    version: "0.1.302-1",
    iso_version: "0.1.302",
    iso_filename: "virtio-win-0.1.302.iso",
    iso_url: "https://example.invalid/virtio-win-0.1.302.iso",
    iso_size: 877373440,
    is_stable: true,
    checksum: "",
    checksum_algorithm: "",
    published_at: null,
    discovered_at: "2026-08-31T00:00:00Z",
  },
  {
    version: "0.1.285-1",
    iso_version: "0.1.285",
    iso_filename: "virtio-win-0.1.285.iso",
    iso_url: "https://example.invalid/virtio-win-0.1.285.iso",
    iso_size: 0,
    is_stable: false,
    checksum: "",
    checksum_algorithm: "",
    published_at: null,
    discovered_at: "2026-05-01T00:00:00Z",
  },
];

const storages = [
  {
    id: "s1",
    cluster_id: "c1",
    node_id: "n1",
    storage: "local",
    type: "dir",
    content: "iso,vztmpl,backup",
    active: true,
    enabled: true,
    shared: false,
    total: 0,
    used: 0,
    avail: 0,
    last_seen_at: "",
    created_at: "",
    updated_at: "",
  },
  {
    id: "s2",
    cluster_id: "c1",
    node_id: "n1",
    storage: "vmdata",
    type: "rbd",
    content: "images,rootdir",
    active: true,
    enabled: true,
    shared: true,
    total: 0,
    used: 0,
    avail: 0,
    last_seen_at: "",
    created_at: "",
    updated_at: "",
  },
];

function mockHooks(
  config: VirtioWinConfig,
  opts: { mutate?: ReturnType<typeof vi.fn>; canManage?: boolean } = {},
) {
  const mutate = opts.mutate ?? vi.fn();
  vi.mocked(useVirtioWinConfig).mockReturnValue({
    data: config,
    isLoading: false,
  } as unknown as ReturnType<typeof useVirtioWinConfig>);
  vi.mocked(useUpdateVirtioWinConfig).mockReturnValue({
    mutate,
    isPending: false,
    error: null,
  } as unknown as ReturnType<typeof useUpdateVirtioWinConfig>);
  vi.mocked(useVirtioWinReleases).mockReturnValue({
    data: releases,
    isLoading: false,
  } as unknown as ReturnType<typeof useVirtioWinReleases>);
  vi.mocked(useDownloadVirtioWin).mockReturnValue({
    mutate: vi.fn(),
    isPending: false,
    error: null,
  } as unknown as ReturnType<typeof useDownloadVirtioWin>);
  vi.mocked(useClusterStorage).mockReturnValue({
    data: storages,
    isLoading: false,
  } as unknown as ReturnType<typeof useClusterStorage>);
  vi.mocked(usePermissions).mockReturnValue({
    canManage: () => opts.canManage ?? true,
  } as unknown as ReturnType<typeof usePermissions>);
  return mutate;
}

describe("VirtioWinConfigCard", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("offers only ISO-capable storages as a target", async () => {
    mockHooks(baseConfig);
    const user = userEvent.setup();
    renderWithProviders(<VirtioWinConfigCard clusterId="c1" />);

    // First combobox is the storage select.
    const storageSelect = screen.getAllByRole("combobox")[0];
    expect(storageSelect).toBeDefined();
    await user.click(storageSelect as HTMLElement);

    expect(screen.getByRole("option", { name: /local/ })).toBeInTheDocument();
    // A pool whose content is images,rootdir can never hold an ISO; offering it
    // would produce a save that succeeds and a download that always fails.
    expect(
      screen.queryByRole("option", { name: /vmdata/ }),
    ).not.toBeInTheDocument();
  });

  it("lists a non-shared pool once, not once per node", async () => {
    // storage_pools is keyed (cluster, node, storage), so a `local` dir pool on
    // a three-node cluster arrives as three rows with the same storage name.
    const perNode = [0, 1, 2].map((i) => ({
      ...storages[0],
      id: `s-local-${String(i)}`,
      node_id: `n${String(i)}`,
    }));
    mockHooks(baseConfig);
    // After mockHooks, which installs the default storage list.
    vi.mocked(useClusterStorage).mockReturnValue({
      data: perNode,
      isLoading: false,
    } as unknown as ReturnType<typeof useClusterStorage>);

    const user = userEvent.setup();
    renderWithProviders(<VirtioWinConfigCard clusterId="c1" />);

    const storageSelect = screen.getAllByRole("combobox")[0];
    await user.click(storageSelect as HTMLElement);

    expect(screen.getAllByRole("option", { name: /local/ })).toHaveLength(1);
  });

  it("sends the prune flag explicitly so a save never silently disarms it", async () => {
    const mutate = mockHooks({ ...baseConfig, prune_enabled: true });
    const user = userEvent.setup();
    renderWithProviders(<VirtioWinConfigCard clusterId="c1" />);

    await user.click(screen.getByRole("button", { name: /^Save$/ }));

    expect(mutate).toHaveBeenCalledTimes(1);
    const body = firstCallBody(mutate);
    // Present and true — an omitted key would preserve the stored value, but a
    // form the operator actually saw must assert what it displayed.
    expect(body.prune_enabled).toBe(true);
  });

  it("round-trips a node pinned through the API even though the form cannot set one", async () => {
    const mutate = mockHooks({ ...baseConfig, node: "pve3" });
    const user = userEvent.setup();
    renderWithProviders(<VirtioWinConfigCard clusterId="c1" />);

    await user.click(screen.getByRole("button", { name: /^Save$/ }));

    const body = firstCallBody(mutate);
    expect(body.node).toBe("pve3");
  });

  it("surfaces the last check error", () => {
    mockHooks({
      ...baseConfig,
      last_error: "403 Permission check failed (Sys.AccessNetwork)",
    });
    renderWithProviders(<VirtioWinConfigCard clusterId="c1" />);

    expect(screen.getByText(/Last check failed/i)).toBeInTheDocument();
    expect(screen.getByText(/Sys.AccessNetwork/)).toBeInTheDocument();
  });

  it("disables editing without manage:storage", () => {
    mockHooks(baseConfig, { canManage: false });
    renderWithProviders(<VirtioWinConfigCard clusterId="c1" />);

    expect(screen.getByRole("button", { name: /^Save$/ })).toBeDisabled();
    expect(
      screen.getByRole("button", { name: /Download now/i }),
    ).toBeDisabled();
  });

  it("explains that a pinned version stops tracking upstream", () => {
    mockHooks({ ...baseConfig, target_version: "0.1.285-1" });
    renderWithProviders(<VirtioWinConfigCard clusterId="c1" />);

    expect(screen.getByText(/Pinned\./)).toBeInTheDocument();
  });
});
