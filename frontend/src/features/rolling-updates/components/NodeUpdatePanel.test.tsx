import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import type { ReactNode } from "react";

import { NodeUpdatePanel } from "./NodeUpdatePanel";
import type { AptPackage } from "@/types/api";

const CLUSTER = "cccccccc-0000-0000-0000-000000000001";
const NODE = "pve-01";
const PACKAGES_PATH = `/api/v1/clusters/${CLUSTER}/nodes/${NODE}/packages`;
const SSH_PATH = `/api/v1/clusters/${CLUSTER}/ssh-credentials`;

const listMock = vi.fn();
const getMock = vi.fn();
const postMock = vi.fn();

// Spread the real module: api-error.ts imports ApiClientError from here, and a
// mock that only supplies apiClient makes describeError throw on every render.
vi.mock("@/lib/api-client", async () => {
  const actual =
    await vi.importActual<typeof import("@/lib/api-client")>(
      "@/lib/api-client",
    );
  return {
    ...actual,
    apiClient: {
      list: (path: string) => listMock(path) as unknown,
      get: (path: string) => getMock(path) as unknown,
      post: (path: string, body: unknown) => postMock(path, body) as unknown,
    },
  };
});

let canManage = true;
vi.mock("@/hooks/useAuth", () => ({
  useAuth: () => ({ canManage: () => canManage }),
}));

function pkg(name: string, overrides: Partial<AptPackage> = {}): AptPackage {
  return {
    Package: name,
    Title: name,
    Description: "",
    Version: "2.0",
    OldVersion: "1.0",
    Priority: "optional",
    Section: "admin",
    Origin: "Debian",
    Arch: "amd64",
    ...overrides,
  } as AptPackage;
}

function renderPanel() {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={qc}>
      <MemoryRouter>{children}</MemoryRouter>
    </QueryClientProvider>
  );
  return render(<NodeUpdatePanel clusterId={CLUSTER} nodeName={NODE} />, {
    wrapper,
  });
}

/** Two pending packages and SSH configured — the state the button needs. */
function serveReady() {
  listMock.mockImplementation((path: string) =>
    path === PACKAGES_PATH
      ? Promise.resolve([pkg("pve-manager"), pkg("libc6")])
      : Promise.resolve([]),
  );
  getMock.mockImplementation((path: string) =>
    path === SSH_PATH
      ? Promise.resolve({ cluster_id: CLUSTER, username: "root", port: 22 })
      : Promise.resolve(null),
  );
}

beforeEach(() => {
  listMock.mockReset();
  getMock.mockReset();
  postMock.mockReset();
  postMock.mockResolvedValue({ id: "job-1" });
  canManage = true;
});

describe("NodeUpdatePanel", () => {
  it("lists the pending packages", async () => {
    serveReady();
    renderPanel();

    expect(await screen.findByText("pve-manager")).toBeInTheDocument();
    expect(screen.getByText("libc6")).toBeInTheDocument();
  });

  // Disruptive: it runs apt on a live hypervisor. Nothing may be created on the
  // first click.
  it("confirms before creating anything", async () => {
    serveReady();
    renderPanel();

    await userEvent.click(
      await screen.findByRole("button", { name: /Update this node/ }),
    );

    expect(postMock).not.toHaveBeenCalled();
    expect(
      await screen.findByText(`Update ${NODE} in place?`),
    ).toBeInTheDocument();
  });

  /**
   * THE POINT OF THE FEATURE. drain_guests must be false and the job must name
   * exactly this node.
   *
   * If drain_guests went out true (or was dropped, which the server reads as
   * true) the orchestrator would migrate every guest off the node first — and
   * on a single-node cluster it would not even get that far: the drain fails
   * with "no available target nodes for migration". Either way the button
   * silently does something other than what its dialog just promised.
   */
  it("creates an in-place job for this node alone", async () => {
    serveReady();
    renderPanel();

    await userEvent.click(
      await screen.findByRole("button", { name: /Update this node/ }),
    );
    await userEvent.click(
      await screen.findByRole("button", { name: "Update in place" }),
    );

    await waitFor(() => {
      expect(postMock).toHaveBeenCalledWith(
        `/api/v1/clusters/${CLUSTER}/rolling-updates`,
        expect.objectContaining({
          nodes: [NODE],
          parallelism: 1,
          drain_guests: false,
          auto_upgrade: true,
          reboot_after_update: false,
        }),
      );
    });
  });

  it("starts the job it created", async () => {
    serveReady();
    renderPanel();

    await userEvent.click(
      await screen.findByRole("button", { name: /Update this node/ }),
    );
    await userEvent.click(
      await screen.findByRole("button", { name: "Update in place" }),
    );

    await waitFor(() => {
      expect(postMock).toHaveBeenCalledWith(
        `/api/v1/clusters/${CLUSTER}/rolling-updates/job-1/start`,
        undefined,
      );
    });
  });

  /**
   * THE ORPHAN-JOB REGRESSION. The panel lives inside a Radix tab, which
   * unmounts it the moment the operator switches tabs — and TanStack only runs
   * per-call mutation callbacks while the observer still has listeners. When
   * the start was chained through the create's `onSuccess`, switching tabs
   * during the POST left the job created and never started.
   *
   * A 'pending' job counts as active in HasRunningJobForCluster, so that orphan
   * makes every later rolling update on the cluster — this panel and the
   * cluster-wide wizard both — refuse with "already active", until someone
   * finds and cancels a job they never knew existed.
   */
  it("still starts the job when the panel unmounts mid-flight", async () => {
    serveReady();
    let releaseCreate: (v: { id: string }) => void = () => undefined;
    postMock.mockImplementationOnce(
      () =>
        new Promise<{ id: string }>((resolve) => {
          releaseCreate = resolve;
        }),
    );
    postMock.mockResolvedValue({ id: "job-1" });

    const view = renderPanel();
    await userEvent.click(
      await screen.findByRole("button", { name: /Update this node/ }),
    );
    await userEvent.click(
      await screen.findByRole("button", { name: "Update in place" }),
    );
    await waitFor(() => {
      expect(postMock).toHaveBeenCalledTimes(1);
    });

    // Operator switches away while the create is still in flight.
    view.unmount();
    releaseCreate({ id: "job-1" });

    await waitFor(() => {
      expect(postMock).toHaveBeenCalledWith(
        `/api/v1/clusters/${CLUSTER}/rolling-updates/job-1/start`,
        undefined,
      );
    });
  });

  // The upgrade runs over SSH. Offering the button without credentials would
  // produce a job that fails at the first node.
  it("disables the button when the cluster has no SSH credentials", async () => {
    listMock.mockResolvedValue([pkg("pve-manager")]);
    getMock.mockResolvedValue(null);
    renderPanel();

    expect(
      await screen.findByRole("button", { name: /Update this node/ }),
    ).toBeDisabled();
    expect(screen.getByText(/needs SSH credentials/)).toBeInTheDocument();
  });

  /**
   * Reading the credentials needs manage:ssh_credentials, which an operator
   * holding only manage:rolling_update lacks. Treating that 403 as "not
   * configured" would state something false about the cluster and disable the
   * button over it.
   */
  it("does not claim SSH is missing when it could not check", async () => {
    listMock.mockResolvedValue([pkg("pve-manager")]);
    getMock.mockRejectedValue(new Error("Insufficient permissions"));
    renderPanel();

    expect(
      await screen.findByRole("button", { name: /Update this node/ }),
    ).toBeEnabled();
    expect(screen.queryByText(/needs SSH credentials/)).not.toBeInTheDocument();
  });

  it("offers nothing to apply when the node is up to date", async () => {
    listMock.mockResolvedValue([]);
    getMock.mockResolvedValue({ cluster_id: CLUSTER });
    renderPanel();

    expect(await screen.findByText(/Up to date/)).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: /Update this node/ }),
    ).not.toBeInTheDocument();
  });

  /**
   * A failed read must never render as "up to date". This badge is what an
   * operator reads before deciding a node is patched, and a token missing
   * Sys.Audit fails the read rather than returning an empty list.
   */
  it("does not report a failed read as up to date", async () => {
    listMock.mockRejectedValue(new Error("permission denied"));
    getMock.mockResolvedValue(null);
    renderPanel();

    expect(
      await screen.findByText(/Could not load pending updates/),
    ).toBeInTheDocument();
    expect(screen.queryByText(/Up to date/)).not.toBeInTheDocument();
  });

  it("hides the action from a user who cannot manage rolling updates", async () => {
    canManage = false;
    serveReady();
    renderPanel();

    expect(await screen.findByText("pve-manager")).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: /Update this node/ }),
    ).not.toBeInTheDocument();
  });
});
