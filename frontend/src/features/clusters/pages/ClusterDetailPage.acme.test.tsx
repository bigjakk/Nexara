import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes, useNavigate } from "react-router-dom";
import { toast } from "sonner";

import { ApiClientError } from "@/lib/api-client";
import { createAppQueryClient } from "@/test/app-query-client";
import { useAuthStore } from "@/stores/auth-store";
import type { NodeACMEConfig } from "@/features/acme/api/acme-queries";
import type { ClusterResponse } from "@/types/api";
import { ClusterDetailPage } from "./ClusterDetailPage";

/**
 * The route clusters/:clusterId is one page across every cluster, and the search
 * bar's /clusters/{id}?tab=certificates leaves the Certificates tab selected, so
 * moving from one cluster to another keeps the tab on the page: what it holds —
 * the node it was on, a dialog, a save still in flight — survives into the next
 * cluster unless the page keys its header, banner and tabs on the cluster (one
 * key, in ClusterDetailPage.tsx). These run the real page through the real
 * router.
 */

const listMock = vi.fn();
const getMock = vi.fn();
const putMock = vi.fn();

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
      put: (path: string, body: unknown) => putMock(path, body) as unknown,
      post: vi.fn(),
      delete: vi.fn(),
    },
  };
});

// The app's mutation-error net toasts through sonner, so this one mock sees
// every toast a run can raise, of any kind.
vi.mock("sonner", () => ({
  toast: Object.assign(vi.fn(), {
    success: vi.fn(),
    info: vi.fn(),
    warning: vi.fn(),
    error: vi.fn(),
    message: vi.fn(),
    loading: vi.fn(),
  }),
}));

// Tabs this page imports and these tests never show.
vi.mock("../components/ClusterCephTab", () => ({
  ClusterCephTab: () => <div>Ceph Tab</div>,
}));
vi.mock("../components/ClusterNetworksTab", () => ({
  ClusterNetworksTab: () => <div>Networks Tab</div>,
}));
vi.mock("../components/ClusterFirewallTab", () => ({
  ClusterFirewallTab: () => <div>Firewall Tab</div>,
}));
vi.mock("../components/ClusterDRSTab", () => ({
  ClusterDRSTab: () => <div>DRS Tab</div>,
}));

const toastSpies = {
  default: vi.mocked(toast),
  success: vi.mocked(toast.success),
  info: vi.mocked(toast.info),
  warning: vi.mocked(toast.warning),
  error: vi.mocked(toast.error),
  message: vi.mocked(toast.message),
  loading: vi.mocked(toast.loading),
};

/** Every toast raised so far, of any kind, as "kind: message". */
function toastsRaised(): string[] {
  return Object.entries(toastSpies).flatMap(([kind, spy]) =>
    (spy.mock.calls as unknown[][]).map(
      (args) => `${kind}: ${String(args[0])}`,
    ),
  );
}

const CLUSTER_A = "cccccccc-0000-0000-0000-0000000000a1";
const CLUSTER_B = "cccccccc-0000-0000-0000-0000000000b2";
const DENIED = "Proxmox API permission denied";

function cluster(id: string, name: string): ClusterResponse {
  return {
    id,
    name,
    api_url: "https://pve.example.com:8006",
    token_id: "nexara@pve!token",
    tls_fingerprint: "",
    sync_interval_seconds: 60,
    is_active: true,
    status: "online",
    pve_version: "",
    credential_source: "manual",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  };
}

function nodesPath(clusterId: string) {
  return `/api/v1/clusters/${clusterId}/nodes`;
}

function configPath(clusterId: string, node: string) {
  return `/api/v1/clusters/${clusterId}/nodes/${node}/acme-config`;
}

/**
 * What the transport serves: `nodes` lists the nodes of each cluster. A's pve-01
 * holds two domains and its pve-02 one; B's pve-01, pve-02 and pve-07 one each,
 * none of them A's.
 */
function serve(nodes: { a: string[]; b: string[] }) {
  const configs: Record<string, NodeACMEConfig> = {
    [configPath(CLUSTER_A, "pve-01")]: {
      acmedomain0: "domain=node1.example.com",
      acmedomain1: "domain=node2.example.com",
      digest: "d1",
    },
    [configPath(CLUSTER_A, "pve-02")]: {
      acmedomain0: "domain=node3.example.com",
      digest: "d2",
    },
    [configPath(CLUSTER_B, "pve-01")]: {
      acmedomain0: "domain=other.example.com",
      digest: "dB",
    },
    [configPath(CLUSTER_B, "pve-02")]: {
      acmedomain0: "domain=elsewhere.example.com",
      digest: "dB2",
    },
    [configPath(CLUSTER_B, "pve-07")]: {
      acmedomain0: "domain=seven.example.com",
      digest: "d7",
    },
  };
  const names = (list: string[]) =>
    list.map((name) => ({ name, node_name: name }));
  listMock.mockImplementation((path: string) => {
    if (path === nodesPath(CLUSTER_A)) return Promise.resolve(names(nodes.a));
    if (path === nodesPath(CLUSTER_B)) return Promise.resolve(names(nodes.b));
    return Promise.resolve([]);
  });
  getMock.mockImplementation((path: string) => {
    if (path === `/api/v1/clusters/${CLUSTER_A}`) {
      return Promise.resolve(cluster(CLUSTER_A, "Cluster A"));
    }
    if (path === `/api/v1/clusters/${CLUSTER_B}`) {
      return Promise.resolve(cluster(CLUSTER_B, "Cluster B"));
    }
    return Promise.resolve(configs[path] ?? null);
  });
}

function reads(path: string): number {
  return getMock.mock.calls.filter((c) => c[0] === path).length;
}

function deferred<T>() {
  let resolve: (value: T) => void = () => undefined;
  let reject: (reason: unknown) => void = () => undefined;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

/** Lets whatever is already queued run, and React draw what it set. */
async function flush(): Promise<void> {
  await act(async () => {
    await new Promise<void>((resolve) => {
      setTimeout(resolve, 0);
    });
  });
}

/** Hands the router's navigate to the test, to move the page from one cluster to another. */
let navigateTo: (path: string) => void = () => undefined;

function Navigator() {
  const navigate = useNavigate();
  navigateTo = (path) => {
    void navigate(path);
  };
  return null;
}

function renderPage() {
  const qc = createAppQueryClient();
  const invalidations = vi.spyOn(qc, "invalidateQueries");
  render(
    <QueryClientProvider client={qc}>
      <MemoryRouter
        initialEntries={[`/clusters/${CLUSTER_A}?tab=certificates`]}
      >
        <Navigator />
        <Routes>
          <Route path="/clusters/:clusterId" element={<ClusterDetailPage />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return { qc, invalidations };
}

type UserEvent = ReturnType<typeof userEvent.setup>;

/** Opens the edit dialog on the first domain, saves it with the request held, and cancels the dialog. */
async function saveFirstDomainHeldThenCancel(user: UserEvent) {
  const rows = await screen.findAllByRole("button", { name: "Edit" });
  const first = rows[0];
  if (!first) throw new Error("no Edit button");
  await user.click(first);
  await user.click(screen.getByRole("button", { name: "Save" }));
  expect(
    await screen.findByRole("button", { name: "Saving..." }),
  ).toBeDisabled();
  await user.click(screen.getByRole("button", { name: "Cancel" }));
  await waitFor(() => {
    expect(screen.queryByRole("dialog")).toBeNull();
  });
}

/** Picks the Node Certificates tab of the ACME tab, and waits for the node's domains. */
async function openNodeCertificates(user: UserEvent, firstDomain: string) {
  await user.click(
    await screen.findByRole("tab", { name: "Node Certificates" }),
  );
  expect(await screen.findByText(firstDomain)).toBeInTheDocument();
}

/** Chooses a node in the selector, and waits for its first domain. */
async function chooseNode(user: UserEvent, node: string, firstDomain: string) {
  await user.click(screen.getByRole("combobox"));
  await user.click(await screen.findByRole("option", { name: node }));
  expect(await screen.findByText(firstDomain)).toBeInTheDocument();
}

/**
 * The two ways the tab can be on a node. By default it is on the first of the
 * list, and while the second cluster's list loads that node is none for a moment,
 * which lets go of a save in flight whatever the page does. A node chosen in the
 * selector never moves, and then only the page's key stands between the first
 * cluster's save and the second cluster's card: the second cluster has the same
 * node, so the tab has nothing to tell it that anything changed.
 */
const NODE_CHOICES: {
  name: string;
  nodes: { a: string[]; b: string[] };
  choose: string | null;
  node: string;
  saved: string;
  theirs: string;
}[] = [
  {
    name: "the first node, which the tab uses unless told otherwise",
    nodes: { a: ["pve-01"], b: ["pve-01"] },
    choose: null,
    node: "pve-01",
    saved: "node1.example.com",
    theirs: "other.example.com",
  },
  {
    name: "a node chosen in the selector",
    nodes: { a: ["pve-01", "pve-02"], b: ["pve-02", "pve-03"] },
    choose: "pve-02",
    node: "pve-02",
    saved: "node3.example.com",
    theirs: "elsewhere.example.com",
  },
];

/** Moves the page to cluster B, the way the search bar does, with the tab still selected. */
async function goToClusterB() {
  act(() => {
    navigateTo(`/clusters/${CLUSTER_B}?tab=certificates`);
  });
  expect(
    await screen.findByRole("heading", { name: "Cluster B" }),
  ).toBeInTheDocument();
}

beforeEach(() => {
  listMock.mockReset();
  getMock.mockReset();
  putMock.mockReset();
  putMock.mockResolvedValue({ status: "ok" });
  for (const spy of Object.values(toastSpies)) spy.mockReset();
  useAuthStore.setState({ user: null, permissions: ["manage:certificate"] });
});

afterEach(() => {
  useAuthStore.setState({ user: null, permissions: [] });
});

describe("the Certificates tab when the page moves to another cluster", () => {
  it.each(NODE_CHOICES)(
    "toasts, once and naming the node, a save of the first cluster that fails after the move, and shows nothing of it on the second's card, on $name",
    async ({ nodes, choose, node, saved, theirs }) => {
      const user = userEvent.setup();
      serve(nodes);
      const held = deferred<unknown>();
      putMock.mockReturnValueOnce(held.promise);
      renderPage();
      await openNodeCertificates(user, "node1.example.com");
      if (choose !== null) await chooseNode(user, choose, saved);

      await saveFirstDomainHeldThenCancel(user);

      await goToClusterB();
      await openNodeCertificates(user, theirs);
      held.reject(
        new ApiClientError(403, { error: "forbidden", message: DENIED }),
      );

      await waitFor(() => {
        expect(toastsRaised()).toEqual([
          `error: Saving the ACME domain ${saved} on ${node} failed: ${DENIED}`,
        ]);
      });
      await flush();
      expect(toastsRaised()).toHaveLength(1);
      // The second cluster's card is its own: its domain, and none of the first's failure.
      expect(screen.getByText(theirs)).toBeInTheDocument();
      expect(screen.queryByText(DENIED)).toBeNull();
    },
  );

  it.each(NODE_CHOICES)(
    "invalidates what the first cluster's save was made to when it lands after the move, not the second cluster's, on $name",
    async ({ nodes, choose, node, saved, theirs }) => {
      const user = userEvent.setup();
      serve(nodes);
      const held = deferred<unknown>();
      putMock.mockReturnValueOnce(held.promise);
      const { invalidations } = renderPage();
      await openNodeCertificates(user, "node1.example.com");
      if (choose !== null) await chooseNode(user, choose, saved);

      await saveFirstDomainHeldThenCancel(user);

      await goToClusterB();
      await openNodeCertificates(user, theirs);
      held.resolve({ status: "ok" });

      await waitFor(() => {
        expect(
          invalidations.mock.calls.map((c) => c[0]?.queryKey),
        ).toContainEqual(["clusters", CLUSTER_A, "nodes", node]);
      });
      await flush();
      expect(
        invalidations.mock.calls.map((c) => c[0]?.queryKey),
      ).not.toContainEqual(["clusters", CLUSTER_B, "nodes", node]);
      expect(toastsRaised()).toEqual([]);
    },
  );

  it("does not read a node of the first cluster from the second, because it was the one chosen there", async () => {
    const user = userEvent.setup();
    serve({ a: ["pve-01", "pve-02"], b: ["pve-07"] });
    renderPage();
    await openNodeCertificates(user, "node1.example.com");
    await chooseNode(user, "pve-02", "node3.example.com");

    await goToClusterB();
    await openNodeCertificates(user, "seven.example.com");
    await flush();

    // The control: the second cluster's own node is read, so that the absence
    // below is not a tab that read nothing.
    expect(reads(configPath(CLUSTER_B, "pve-07"))).toBeGreaterThan(0);
    expect(reads(configPath(CLUSTER_B, "pve-02"))).toBe(0);
  });
});
