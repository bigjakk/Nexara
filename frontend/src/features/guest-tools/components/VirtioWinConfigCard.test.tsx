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
  useCheckVirtioWinNow,
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
  useCheckVirtioWinNow: vi.fn(),
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
  check_schedule: "",
  check_timezone: "",
  next_check_at: null,
  effective_version: "0.1.302-1",
  source_url: "https://fedorapeople.org/groups/virt/virtio-win/direct-downloads",
};

const releases: VirtioWinRelease[] = [
  {
    version: "0.1.302-1",
    iso_version: "0.1.302",
    iso_filename: "virtio-win-0.1.302.iso",
    iso_url: "https://example.invalid/virtio-win-0.1.302.iso",
    iso_size: 877373440,
    is_stable: true,
    discovered_at: "2026-08-31T00:00:00Z",
  },
  {
    version: "0.1.285-1",
    iso_version: "0.1.285",
    iso_filename: "virtio-win-0.1.285.iso",
    iso_url: "https://example.invalid/virtio-win-0.1.285.iso",
    iso_size: 0,
    is_stable: false,
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
  opts: {
    mutate?: ReturnType<typeof vi.fn>;
    checkMutate?: ReturnType<typeof vi.fn>;
    canManage?: boolean;
  } = {},
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
  vi.mocked(useCheckVirtioWinNow).mockReturnValue({
    mutate: opts.checkMutate ?? vi.fn(),
    isPending: false,
    error: null,
  } as unknown as ReturnType<typeof useCheckVirtioWinNow>);
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

    await user.click(screen.getByLabelText("Target storage"));

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

    await user.click(screen.getByLabelText("Target storage"));

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

  it("shows both check timestamps, and says which way round they are", () => {
    mockHooks({
      ...baseConfig,
      last_check_at: "2026-09-01T18:12:00Z",
      next_check_at: "2026-09-02T08:00:00Z",
    });
    renderWithProviders(<VirtioWinConfigCard clusterId="c1" />);

    expect(screen.getByText("Last checked")).toBeInTheDocument();
    expect(screen.getByText("Next check")).toBeInTheDocument();
  });

  it("reads a null next check as imminent, not as missing", () => {
    // NULL is how the API says "due now" — a cluster just enabled, or one
    // upgraded in place. Rendering it blank would read as broken.
    mockHooks({ ...baseConfig, next_check_at: null });
    renderWithProviders(<VirtioWinConfigCard clusterId="c1" />);

    expect(screen.getByText(/within a minute/)).toBeInTheDocument();
  });

  it("does not promise a next check while automatic downloads are off", () => {
    mockHooks({ ...baseConfig, enabled: false, next_check_at: null });
    renderWithProviders(<VirtioWinConfigCard clusterId="c1" />);

    expect(screen.getByText(/automatic downloads are off/)).toBeInTheDocument();
  });

  it("saves a daily schedule as cron, with the zone", async () => {
    const mutate = mockHooks({
      ...baseConfig,
      check_schedule: "0 3 * * *",
      check_timezone: "America/Chicago",
    });
    const user = userEvent.setup();
    renderWithProviders(<VirtioWinConfigCard clusterId="c1" />);

    await user.click(screen.getByRole("button", { name: /^Save$/ }));

    const body = firstCallBody(mutate);
    expect(body.check_schedule).toBe("0 3 * * *");
    expect(body.check_timezone).toBe("America/Chicago");
  });

  it("drops the zone when the schedule is the plain interval", async () => {
    // A zone means nothing without a cron to read in it, and storing one would
    // leave a stale value behind the next time a schedule is set.
    const mutate = mockHooks({
      ...baseConfig,
      check_schedule: "",
      check_timezone: "America/Chicago",
    });
    const user = userEvent.setup();
    renderWithProviders(<VirtioWinConfigCard clusterId="c1" />);

    await user.click(screen.getByRole("button", { name: /^Save$/ }));

    const body = firstCallBody(mutate);
    expect(body.check_schedule).toBe("");
    expect(body.check_timezone).toBe("");
  });

  it("keeps an unsaved schedule edit when a refetch only moves the timestamps", async () => {
    // Check now writes last_check_at/next_check_at, and saving the download
    // source below the card changes source_url — neither touches a field this
    // form shows. Re-seeding on either would silently revert a half-made edit,
    // and the next Save would write the OLD schedule back.
    const mutate = vi.fn();
    mockHooks(baseConfig, { mutate });
    const user = userEvent.setup();
    const { rerender } = renderWithProviders(
      <VirtioWinConfigCard clusterId="c1" />,
    );

    await user.click(screen.getByLabelText("Check schedule"));
    await user.click(screen.getByRole("option", { name: "Daily at" }));

    // A refetch that changed only the read-only fields.
    mockHooks(
      {
        ...baseConfig,
        last_check_at: "2026-09-01T18:12:00Z",
        next_check_at: "2026-09-02T08:00:00Z",
        source_url: "http://mirror.internal/virtio-win",
      },
      { mutate },
    );
    rerender(<VirtioWinConfigCard clusterId="c1" />);

    await user.click(screen.getByRole("button", { name: /^Save$/ }));
    expect(firstCallBody(mutate).check_schedule).toBe("0 3 * * *");
  });

  it("re-seeds when the config's own form fields change underneath", async () => {
    // The other half of the rule: an external change to a field the form shows
    // must still land, or two operators would silently overwrite each other.
    const mutate = vi.fn();
    mockHooks(baseConfig, { mutate });
    const user = userEvent.setup();
    const { rerender } = renderWithProviders(
      <VirtioWinConfigCard clusterId="c1" />,
    );

    mockHooks({ ...baseConfig, check_schedule: "30 2 * * 0" }, { mutate });
    rerender(<VirtioWinConfigCard clusterId="c1" />);

    await user.click(screen.getByRole("button", { name: /^Save$/ }));
    expect(firstCallBody(mutate).check_schedule).toBe("30 2 * * 0");
  });

  it("offers no check to run when automatic downloads are off", () => {
    // There is no cycle to trigger, and running one would dispatch an ~840 MB
    // fetch the operator has just switched off. The API refuses it too.
    mockHooks({ ...baseConfig, enabled: false });
    renderWithProviders(<VirtioWinConfigCard clusterId="c1" />);

    expect(screen.getByRole("button", { name: /Check now/i })).toBeDisabled();
    expect(
      screen.getByRole("button", { name: /Download now/i }),
    ).not.toBeDisabled();
  });

  it("runs a check without forcing a download", async () => {
    // The two buttons are not synonyms: Check now reconciles and fetches only
    // if the target is missing, which is how a just-saved schedule or source
    // gets verified without waiting for its next slot.
    const checkMutate = vi.fn();
    mockHooks(baseConfig, { checkMutate });
    const user = userEvent.setup();
    renderWithProviders(<VirtioWinConfigCard clusterId="c1" />);

    await user.click(screen.getByRole("button", { name: /Check now/i }));
    expect(checkMutate).toHaveBeenCalledTimes(1);
  });
});
