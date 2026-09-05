import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/test-utils";
import { apiClient } from "@/lib/api-client";
import { VeeamServerTable } from "./VeeamServerTable";
import type { VeeamProbeResult, VeeamServer } from "../types/backup";

vi.mock("@/lib/api-client", () => ({
  apiClient: { post: vi.fn() },
}));

const mockedPost = vi.mocked(apiClient.post);

function server(over: Partial<VeeamServer> = {}): VeeamServer {
  return {
    id: "veeam-1",
    name: "Veeam Primary",
    base_url: "https://vbr.example.com:9419",
    username: "ad\\jdoe",
    api_revision: "1.3-rev2",
    product_version: "13.1.0.411",
    license_edition: "EnterprisePlus",
    tls_fingerprint: "",
    verify_tls: true,
    enabled: true,
    last_sync_at: "2026-08-26T12:05:00Z",
    last_sync_error: "",
    created_at: "2026-08-26T12:00:00Z",
    updated_at: "2026-08-26T12:00:00Z",
    ...over,
  };
}

function probe(over: Partial<VeeamProbeResult> = {}): VeeamProbeResult {
  return {
    api_revision: "1.3-rev2",
    server_name: "vbr01",
    build_version: "13.1.0.411",
    platform: "Windows",
    license_edition: "EnterprisePlus",
    license_status: "Valid",
    license_type: "NFR",
    licensed_to: "ExampleOrg",
    license_expiration: "2027-04-27T00:00:00Z",
    proxmox_clusters: [{ name: "cluster01", vm_count: 14 }],
    warnings: [],
    ...over,
  };
}

describe("VeeamServerTable", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("shows version and edition without expanding", () => {
    renderWithProviders(
      <VeeamServerTable
        servers={[server()]}
        onEdit={vi.fn()}
        onDelete={vi.fn()}
      />,
    );

    expect(screen.getByText("Veeam Primary")).toBeInTheDocument();
    expect(screen.getByText("13.1.0.411")).toBeInTheDocument();
    expect(screen.getByText("EnterprisePlus")).toBeInTheDocument();
    expect(screen.getByText("Connected")).toBeInTheDocument();
  });

  it("reads Syncing, not Connected, before the first inventory pass lands", () => {
    renderWithProviders(
      <VeeamServerTable
        servers={[server({ last_sync_at: null })]}
        onEdit={vi.fn()}
        onDelete={vi.fn()}
      />,
    );

    // A just-registered server is enabled with no error, which used to fall
    // through to a green "Connected" over empty tables — the UI claiming
    // health while the operator saw nothing and assumed it was broken.
    expect(screen.getByText("Syncing")).toBeInTheDocument();
    expect(screen.queryByText("Connected")).not.toBeInTheDocument();
  });

  it("reads Error when the first pass failed, rather than sitting on Syncing", () => {
    renderWithProviders(
      <VeeamServerTable
        servers={[server({ last_sync_at: null, last_sync_error: "401" })]}
        onEdit={vi.fn()}
        onDelete={vi.fn()}
      />,
    );

    expect(screen.getByText("Error")).toBeInTheDocument();
    expect(screen.queryByText("Syncing")).not.toBeInTheDocument();
  });

  it("reveals connection detail when the row is expanded", async () => {
    const user = userEvent.setup();
    renderWithProviders(
      <VeeamServerTable
        servers={[server()]}
        onEdit={vi.fn()}
        onDelete={vi.fn()}
      />,
    );

    expect(screen.queryByText("1.3-rev2")).not.toBeInTheDocument();
    await user.click(screen.getByText("Veeam Primary"));

    expect(screen.getByText("1.3-rev2")).toBeInTheDocument();
    expect(screen.getByText("ad\\jdoe")).toBeInTheDocument();
    expect(screen.getByText("Verified against system CAs")).toBeInTheDocument();
  });

  it("renders the probe result, including the covered Proxmox clusters", async () => {
    const user = userEvent.setup();
    mockedPost.mockResolvedValue(probe());

    renderWithProviders(
      <VeeamServerTable
        servers={[server()]}
        onEdit={vi.fn()}
        onDelete={vi.fn()}
      />,
    );
    await user.click(screen.getByTitle("Test connection"));

    expect(await screen.findByText(/Connected to vbr01/)).toBeInTheDocument();
    // The cluster list is the point of the test: a licensed, reachable server
    // with no Proxmox workloads will never produce a row.
    expect(screen.getByText(/cluster01 · 14 VMs/)).toBeInTheDocument();
    expect(mockedPost).toHaveBeenCalledWith(
      "/api/v1/veeam-servers/veeam-1/test",
    );
  });

  it("surfaces probe warnings rather than reporting a clean pass", async () => {
    const user = userEvent.setup();
    mockedPost.mockResolvedValue(
      probe({
        license_edition: "Community",
        proxmox_clusters: [],
        warnings: [
          'Licence edition is "Community"; Proxmox VE backup requires EnterprisePlus.',
        ],
      }),
    );

    renderWithProviders(
      <VeeamServerTable
        servers={[server()]}
        onEdit={vi.fn()}
        onDelete={vi.fn()}
      />,
    );
    await user.click(screen.getByTitle("Test connection"));

    expect(
      await screen.findByText(/requires EnterprisePlus/),
    ).toBeInTheDocument();
    expect(screen.getByText("None")).toBeInTheDocument();
  });

  it("shows a failed test as an error on that server's row", async () => {
    const user = userEvent.setup();
    mockedPost.mockRejectedValue(new Error("veeam: authentication failed"));

    renderWithProviders(
      <VeeamServerTable
        servers={[server()]}
        onEdit={vi.fn()}
        onDelete={vi.fn()}
      />,
    );
    await user.click(screen.getByTitle("Test connection"));

    expect(
      await screen.findByText(/veeam: authentication failed/),
    ).toBeInTheDocument();
    expect(screen.queryByText(/Connected to/)).not.toBeInTheDocument();
  });

  it("disables only the tested server's button while its probe is in flight", async () => {
    // Each row's button owns its own mutation and disables on that mutation's
    // isPending, so this pins all three halves: the right row goes disabled,
    // the other does not, and it comes back afterwards. Going back to one
    // shared mutation fails the middle assertion.
    const user = userEvent.setup();
    let settle: (result: VeeamProbeResult) => void = () => undefined;
    mockedPost.mockReturnValue(
      new Promise<VeeamProbeResult>((resolve) => {
        settle = resolve;
      }),
    );

    renderWithProviders(
      <VeeamServerTable
        servers={[server(), server({ id: "veeam-2", name: "Veeam Offsite" })]}
        onEdit={vi.fn()}
        onDelete={vi.fn()}
      />,
    );

    const [first, second] = screen.getAllByTitle("Test connection");
    if (first === undefined || second === undefined) {
      throw new Error("expected a test button on each row");
    }

    await user.click(first);
    await waitFor(() => {
      expect(first).toBeDisabled();
    });
    expect(second).not.toBeDisabled();

    settle(probe());
    await waitFor(() => {
      expect(first).not.toBeDisabled();
    });
  });

  it("keeps both results when a second test starts before the first settles", async () => {
    // The regression: every row used to share one mutation instance, and
    // mutate() detaches the observer from the mutation before it, discarding
    // the callbacks that were going to record the first server's answer. The
    // first row's probe completed and then vanished — no result, no error, and
    // the row never opened.
    const user = userEvent.setup();
    const resolvers = new Map<string, (result: VeeamProbeResult) => void>();
    mockedPost.mockImplementation(
      (url: string) =>
        new Promise((resolve) => {
          resolvers.set(url, resolve);
        }),
    );

    renderWithProviders(
      <VeeamServerTable
        servers={[server(), server({ id: "veeam-2", name: "Veeam Offsite" })]}
        onEdit={vi.fn()}
        onDelete={vi.fn()}
      />,
    );

    const [first, second] = screen.getAllByTitle("Test connection");
    if (first === undefined || second === undefined) {
      throw new Error("expected a test button on each row");
    }

    // Both in flight at once, which is the whole scenario.
    await user.click(first);
    await user.click(second);

    const resolveFirst = resolvers.get("/api/v1/veeam-servers/veeam-1/test");
    const resolveSecond = resolvers.get("/api/v1/veeam-servers/veeam-2/test");
    if (resolveFirst === undefined || resolveSecond === undefined) {
      throw new Error("expected a request per server");
    }

    resolveFirst(probe({ server_name: "vbr01" }));
    resolveSecond(probe({ server_name: "vbr02" }));

    // The second one was never in doubt; the first is the regression.
    expect(await screen.findByText(/Connected to vbr02/)).toBeInTheDocument();
    expect(await screen.findByText(/Connected to vbr01/)).toBeInTheDocument();
  });

  it("keeps one server's probe result off another's row", async () => {
    const user = userEvent.setup();
    mockedPost.mockResolvedValue(probe({ server_name: "vbr01" }));

    const servers = [
      server(),
      server({
        id: "veeam-2",
        name: "Veeam Offsite",
        base_url: "https://vbr2.example.com:9419",
      }),
    ];
    renderWithProviders(
      <VeeamServerTable
        servers={servers}
        onEdit={vi.fn()}
        onDelete={vi.fn()}
      />,
    );

    const testButtons = screen.getAllByTitle("Test connection");
    const firstTest = testButtons[0];
    if (firstTest === undefined) {
      throw new Error("expected a test button on the first row");
    }
    await user.click(firstTest);
    await screen.findByText(/Connected to vbr01/);

    // Expanding the second server must not show the first one's result.
    await user.click(screen.getByText("Veeam Offsite"));
    await waitFor(() => {
      expect(screen.getAllByText(/Connected to vbr01/)).toHaveLength(1);
    });
  });

  it("reports a pinned certificate distinctly from CA verification", async () => {
    const user = userEvent.setup();
    renderWithProviders(
      <VeeamServerTable
        servers={[server({ tls_fingerprint: "AB:CD:EF" })]}
        onEdit={vi.fn()}
        onDelete={vi.fn()}
      />,
    );

    await user.click(screen.getByText("Veeam Primary"));
    expect(screen.getByText("Pinned (SHA-256)")).toBeInTheDocument();
    expect(screen.getByText("AB:CD:EF")).toBeInTheDocument();
  });

  it("marks a server with no certificate verification", async () => {
    const user = userEvent.setup();
    renderWithProviders(
      <VeeamServerTable
        servers={[server({ verify_tls: false })]}
        onEdit={vi.fn()}
        onDelete={vi.fn()}
      />,
    );

    await user.click(screen.getByText("Veeam Primary"));
    expect(screen.getByText("Not verified")).toBeInTheDocument();
  });

  it("does not trigger row expansion when an action button is clicked", async () => {
    const user = userEvent.setup();
    const onEdit = vi.fn();

    renderWithProviders(
      <VeeamServerTable
        servers={[server()]}
        onEdit={onEdit}
        onDelete={vi.fn()}
      />,
    );
    await user.click(screen.getByTitle("Edit"));

    expect(onEdit).toHaveBeenCalledTimes(1);
    expect(screen.queryByText("1.3-rev2")).not.toBeInTheDocument();
  });
});
