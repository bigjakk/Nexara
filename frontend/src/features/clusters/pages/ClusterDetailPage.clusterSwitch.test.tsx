import { useState } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClientProvider, type QueryClient } from "@tanstack/react-query";
import {
  MemoryRouter,
  Route,
  Routes,
  useLocation,
  useNavigate,
} from "react-router-dom";

import { createAppQueryClient } from "@/test/app-query-client";
import { listOf, stubApi } from "@/test/fetch-stub";
import { useAuthStore } from "@/stores/auth-store";
import type { ClusterResponse, HealthIssue } from "@/types/api";
import { ClusterDetailPage } from "./ClusterDetailPage";

/**
 * The route clusters/:clusterId is one page across every cluster, and the
 * ?tab= that the search bar's shortcuts leave in the URL keeps the same tab
 * selected through a move from one cluster to another. These run the real page
 * and real tab components through a real router, and move the page from one
 * cluster to the next the way the search bar and the browser's Back button do,
 * to show that what a tab holds — a note typed into a form, a confirmation left
 * open — does not go with it. What the page keeps is in the last block.
 *
 * They run the page in a bare router. In the app the shell rebuilds the whole
 * page when the pathname changes (AppShell keys its error boundary on it), so
 * it would have been rebuilt anyway; here the page has only its own key to
 * stand on, which is what is being tested.
 */

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

// The dialog behind the certificate banner's "Edit manually". The real one
// drags in the whole cluster-edit form; what matters here is what it does
// with the cluster it is given, and the real one seeds its form from that once
// and never looks again (see the banner). So does this: it shows the name it
// was opened with, whatever cluster the page has been moved to since.
vi.mock("../components/EditClusterDialog", () => ({
  EditClusterDialog: function EditClusterDialogStub({
    cluster,
  }: {
    cluster: ClusterResponse;
  }) {
    const [openedFor] = useState(cluster.name);
    return <div data-testid="edit-dialog">Editing {openedFor}</div>;
  },
}));

const CLUSTER_A = "cccccccc-0000-0000-0000-0000000000a1";
const CLUSTER_B = "cccccccc-0000-0000-0000-0000000000b2";
const NAME_A = "cluster01";
const NAME_B = "cluster02";

const FINGERPRINT_ISSUE: HealthIssue = {
  type: "tls_fingerprint_changed",
  severity: "err",
  scope: "cluster",
  target: "",
  summary: "TLS certificate changed",
  detail:
    "pve-01 is presenting a different certificate than the one pinned for " +
    "this cluster. It now presents 112233445566778899aabbcc.",
};

function cluster(
  id: string,
  name: string,
  issues: HealthIssue[] = [],
): ClusterResponse {
  return {
    id,
    name,
    api_url: "https://pve-01.example.com:8006",
    token_id: "nexara@pve!token",
    tls_fingerprint: "",
    sync_interval_seconds: 60,
    is_active: true,
    status: "online",
    pve_version: "",
    credential_source: "manual",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    ...(issues.length > 0 ? { issues } : {}),
  };
}

function path(clusterId: string, rest = ""): string {
  return `/api/v1/clusters/${clusterId}${rest}`;
}

/** What the page itself reads for the two clusters, before any tab reads its own. */
function pageReads(
  a: ClusterResponse,
  b: ClusterResponse,
): Record<string, unknown> {
  return {
    "/api/v1/clusters": listOf([a, b]),
    [path(CLUSTER_A)]: a,
    [path(CLUSTER_B)]: b,
    [path(CLUSTER_A, "/nodes")]: listOf([]),
    [path(CLUSTER_B, "/nodes")]: listOf([]),
    [path(CLUSTER_A, "/vms")]: listOf([]),
    [path(CLUSTER_B, "/vms")]: listOf([]),
  };
}

/** The transport: the page's reads, the tab's reads, and nothing else. */
function serve(extra: Record<string, unknown> = {}) {
  return stubApi({
    ...pageReads(cluster(CLUSTER_A, NAME_A), cluster(CLUSTER_B, NAME_B)),
    ...extra,
  });
}

/** What was sent for one request, in order: the body of each, parsed. */
function bodiesSent(method: string, url: string): unknown[] {
  return vi
    .mocked(fetch)
    .mock.calls.filter(
      ([input, init]) => input === url && (init?.method ?? "GET") === method,
    )
    .map(([, init]) =>
      typeof init?.body === "string"
        ? (JSON.parse(init.body) as unknown)
        : null,
    );
}

function reads(api: { sent: string[] }, url: string): number {
  return api.sent.filter((request) => request === `GET ${url}`).length;
}

type UserEvent = ReturnType<typeof userEvent.setup>;

/** Lets whatever is already queued run, and React draw what it set. */
async function settle(): Promise<void> {
  await act(async () => {
    await new Promise<void>((resolve) => {
      setTimeout(resolve, 50);
    });
  });
}

/** Hands the router's navigate to the test, to move the page from one cluster to another. */
let navigateTo: (to: string) => void = () => undefined;

function Navigator() {
  const navigate = useNavigate();
  navigateTo = (to) => {
    void navigate(to);
  };
  return null;
}

function Where() {
  const location = useLocation();
  return <span data-testid="where">{location.pathname + location.search}</span>;
}

function renderPage(initial: string, prepare?: (qc: QueryClient) => void) {
  const qc = createAppQueryClient();
  prepare?.(qc);
  render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[initial]}>
        <Navigator />
        <Where />
        <Routes>
          <Route path="/clusters/:clusterId" element={<ClusterDetailPage />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return qc;
}

function where(): string {
  return screen.getByTestId("where").textContent;
}

async function heading(name: string): Promise<HTMLElement> {
  // By text, not by role: a modal left open on the page hides the page's
  // headings from role queries, and that is a case being tested.
  return screen.findByText(name, { selector: "h1" });
}

/** Moves the page to the second cluster, the way the search bar does, keeping the tab. */
async function moveToB(tab: string | null): Promise<void> {
  act(() => {
    navigateTo(`/clusters/${CLUSTER_B}${tab === null ? "" : `?tab=${tab}`}`);
  });
  await heading(NAME_B);
}

beforeEach(() => {
  useAuthStore.setState({ user: null, permissions: ["manage:cluster"] });
});

afterEach(() => {
  vi.unstubAllGlobals();
  useAuthStore.setState({ user: null, permissions: [] });
});

describe("the cluster page when it moves to another cluster: Options, Notes", () => {
  const NOTES = /Enter cluster notes/;

  function notesReads() {
    return {
      [path(CLUSTER_A, "/description")]: { description: "Notes of cluster01" },
      [path(CLUSTER_B, "/description")]: { description: "Notes of cluster02" },
    };
  }

  async function notesBox(text: string): Promise<HTMLElement> {
    const box = await screen.findByPlaceholderText(NOTES);
    await waitFor(() => {
      expect(box).toHaveValue(text);
    });
    return box;
  }

  it("saves a note typed on a cluster to that cluster, with what was typed, once", async () => {
    // The control for the next case: that Save can send a note at all.
    const user = userEvent.setup();
    const api = serve(notesReads());
    renderPage(`/clusters/${CLUSTER_A}?tab=options`);
    const box = await notesBox("Notes of cluster01");

    await user.clear(box);
    await user.type(box, "A draft");
    await user.click(screen.getByRole("button", { name: "Save Description" }));

    await waitFor(() => {
      expect(api.writes()).toEqual([`PUT ${path(CLUSTER_A, "/description")}`]);
    });
    expect(bodiesSent("PUT", path(CLUSTER_A, "/description"))).toEqual([
      { description: "A draft" },
    ]);
  });

  it("does not carry a note typed on one cluster over to the next, and sends nothing for it", async () => {
    const user = userEvent.setup();
    const api = serve(notesReads());
    renderPage(`/clusters/${CLUSTER_A}?tab=options`);
    const box = await notesBox("Notes of cluster01");
    await user.clear(box);
    await user.type(box, "A draft");
    expect(
      screen.getByRole("button", { name: "Save Description" }),
    ).toBeEnabled();

    await moveToB("options");

    // Press what the page offers for the note, as someone looking at the
    // second cluster would. Nothing is sent: what could be is the draft.
    await user.click(screen.getByRole("button", { name: "Save Description" }));
    await settle();
    expect(api.writes()).toEqual([]);
    // The note on screen is the second cluster's own, and there is no draft.
    await notesBox("Notes of cluster02");
    expect(
      screen.getByRole("button", { name: "Save Description" }),
    ).toBeDisabled();
  });
});

describe("the cluster page when it moves to another cluster: Metrics", () => {
  const DELETE_A = `DELETE ${path(CLUSTER_A, "/metric-servers/graphite01")}`;

  /**
   * Both clusters have a graphite01, so that a confirmation left open on the
   * first and pressed on the second would delete something there.
   */
  function metricReads() {
    return {
      [path(CLUSTER_A, "/metric-servers")]: listOf([
        {
          id: "graphite01",
          type: "graphite",
          server: "192.0.2.11",
          port: 2003,
        },
        { id: "influx01", type: "influxdb", server: "192.0.2.12", port: 8089 },
      ]),
      [path(CLUSTER_B, "/metric-servers")]: listOf([
        {
          id: "graphite01",
          type: "graphite",
          server: "192.0.2.21",
          port: 2003,
        },
      ]),
    };
  }

  async function askToDeleteGraphiteOnA(user: UserEvent) {
    await user.click(
      await screen.findByRole("button", { name: "Delete graphite01" }),
    );
    const dialog = await screen.findByRole("alertdialog");
    expect(dialog).toHaveTextContent("Delete metric server graphite01?");
    expect(dialog).toHaveTextContent("192.0.2.11:2003");
  }

  it("deletes the server it asked about, on the cluster it asked about, once", async () => {
    // The control for the next case: that the confirmation can delete at all.
    const user = userEvent.setup();
    const api = serve(metricReads());
    renderPage(`/clusters/${CLUSTER_A}?tab=metric-servers`);
    await askToDeleteGraphiteOnA(user);

    await user.click(screen.getByRole("button", { name: "Delete" }));

    await waitFor(() => {
      expect(api.writes()).toEqual([DELETE_A]);
    });
    await settle();
    expect(api.writes()).toEqual([DELETE_A]);
  });

  it("does not carry a delete confirmation over to the next cluster", async () => {
    const user = userEvent.setup();
    serve(metricReads());
    renderPage(`/clusters/${CLUSTER_A}?tab=metric-servers`);
    await askToDeleteGraphiteOnA(user);

    await moveToB("metric-servers");

    // The second cluster's own row is on screen, and the question about the
    // first cluster's server, whose answer would delete the second's, is not.
    expect(await screen.findByText("192.0.2.21")).toBeInTheDocument();
    expect(screen.queryByRole("alertdialog")).toBeNull();
  });
});

describe("the cluster page when it moves to another cluster: VMs", () => {
  const FILTER = /^Search by name/;

  it("does not carry a filter typed into the table over to the next cluster", async () => {
    const user = userEvent.setup();
    serve();
    renderPage(`/clusters/${CLUSTER_A}?tab=vms`);
    const box = await screen.findByPlaceholderText(FILTER);
    await user.type(box, "name:web");
    // The filter is on: the table has read it, and says what it made of it.
    expect(box).toHaveValue("name:web");
    expect(screen.getByText("name:web")).toBeInTheDocument();

    await moveToB("vms");

    // The second cluster's table is its own, with nothing typed into it: a
    // filter that came along would hide its guests behind the first one's.
    expect(screen.getByPlaceholderText(FILTER)).toHaveValue("");
    expect(screen.queryByText("name:web")).toBeNull();
  });
});

describe("the cluster page when it moves to another cluster: the certificate banner", () => {
  /**
   * Both clusters carry the issue, so that the banner is there on both, and
   * the second is already in the cache, so that it is never absent in between:
   * a banner that goes away while the second cluster loads is rebuilt whatever
   * the page does, and would hide what is being tested. A cluster the operator
   * has been to before is read from the cache.
   */
  function warmed(qc: QueryClient) {
    qc.setQueryData(
      ["clusters", CLUSTER_B],
      cluster(CLUSTER_B, NAME_B, [FINGERPRINT_ISSUE]),
    );
  }

  function serveBoth() {
    return stubApi(
      pageReads(
        cluster(CLUSTER_A, NAME_A, [FINGERPRINT_ISSUE]),
        cluster(CLUSTER_B, NAME_B, [FINGERPRINT_ISSUE]),
      ),
    );
  }

  it("opens the edit dialog for the cluster the banner is on", async () => {
    // The control for the next case: that the button opens a dialog at all.
    const user = userEvent.setup();
    serveBoth();
    renderPage(`/clusters/${CLUSTER_A}?tab=ceph`, warmed);

    await user.click(
      await screen.findByRole("button", { name: "Edit manually" }),
    );

    expect(await screen.findByTestId("edit-dialog")).toHaveTextContent(
      `Editing ${NAME_A}`,
    );
  });

  it("does not carry an open edit dialog over to the next cluster", async () => {
    const user = userEvent.setup();
    serveBoth();
    renderPage(`/clusters/${CLUSTER_A}?tab=ceph`, warmed);
    await user.click(
      await screen.findByRole("button", { name: "Edit manually" }),
    );
    expect(await screen.findByTestId("edit-dialog")).toBeInTheDocument();

    await moveToB("ceph");

    // The second cluster's banner is there, and its dialog is not open.
    expect(
      screen.getByRole("button", { name: "Edit manually" }),
    ).toBeInTheDocument();
    expect(screen.queryByTestId("edit-dialog")).toBeNull();
  });
});

describe("the cluster page when it moves to another cluster: what it keeps", () => {
  function metricReads() {
    return {
      [path(CLUSTER_A, "/metric-servers")]: listOf([
        {
          id: "graphite01",
          type: "graphite",
          server: "192.0.2.11",
          port: 2003,
        },
      ]),
      [path(CLUSTER_B, "/metric-servers")]: listOf([
        {
          id: "graphite02",
          type: "graphite",
          server: "192.0.2.21",
          port: 2003,
        },
      ]),
    };
  }

  it("shows the tab chosen, writes it to the URL, and drops the parameter for Overview", async () => {
    const user = userEvent.setup();
    serve({
      ...metricReads(),
      [path(CLUSTER_A, "/description")]: { description: "Notes of cluster01" },
    });
    renderPage(`/clusters/${CLUSTER_A}`);
    await heading(NAME_A);
    expect(
      screen.getByRole("tab", { name: "Overview", selected: true }),
    ).toBeInTheDocument();
    const title = screen.getByRole("heading", { level: 1 });

    await user.click(screen.getByRole("tab", { name: "Metrics" }));
    expect(
      await screen.findByText("Metric Servers", { selector: "div" }),
    ).toBeInTheDocument();
    expect(where()).toBe(`/clusters/${CLUSTER_A}?tab=metric-servers`);

    await user.click(screen.getByRole("tab", { name: "Options" }));
    expect(
      await screen.findByPlaceholderText(/Enter cluster notes/),
    ).toBeVisible();
    expect(where()).toBe(`/clusters/${CLUSTER_A}?tab=options`);

    await user.click(screen.getByRole("tab", { name: "Overview" }));
    expect(
      await screen.findByText("No nodes found for this cluster."),
    ).toBeInTheDocument();
    expect(where()).toBe(`/clusters/${CLUSTER_A}`);

    // Choosing a tab is not a change of cluster: the header is the same one.
    expect(screen.getByRole("heading", { level: 1 })).toBe(title);
  });

  it("keeps the tab selected through the move, as the URL says, and shows the second cluster's content in it", async () => {
    serve(metricReads());
    renderPage(`/clusters/${CLUSTER_A}?tab=metric-servers`);
    expect(await screen.findByText("192.0.2.11")).toBeInTheDocument();
    expect(screen.queryByText("192.0.2.21")).toBeNull();

    await moveToB("metric-servers");

    expect(where()).toBe(`/clusters/${CLUSTER_B}?tab=metric-servers`);
    expect(
      screen.getByRole("tab", { name: "Metrics", selected: true }),
    ).toBeInTheDocument();
    expect(await screen.findByText("192.0.2.21")).toBeInTheDocument();
    expect(screen.queryByText("192.0.2.11")).toBeNull();
  });

  it("rebuilds the tab from the cache when a cluster is left and come back to, reading nothing again that is still fresh", async () => {
    const api = serve(metricReads());
    renderPage(`/clusters/${CLUSTER_A}?tab=metric-servers`);
    expect(await screen.findByText("192.0.2.11")).toBeInTheDocument();
    await moveToB("metric-servers");
    expect(await screen.findByText("192.0.2.21")).toBeInTheDocument();
    // Each cluster's own list has been read, once: so that "not again" below
    // is not a tab that read nothing.
    expect(reads(api, path(CLUSTER_A, "/metric-servers"))).toBe(1);
    expect(reads(api, path(CLUSTER_B, "/metric-servers"))).toBe(1);

    act(() => {
      navigateTo(`/clusters/${CLUSTER_A}?tab=metric-servers`);
    });

    // The tab is built anew for the first cluster and has its rows on the
    // first draw, from the cache, with nothing read to get them.
    expect(screen.getByText("192.0.2.11")).toBeInTheDocument();
    await settle();
    expect(reads(api, path(CLUSTER_A, "/metric-servers"))).toBe(1);
    expect(reads(api, path(CLUSTER_B, "/metric-servers"))).toBe(1);
    expect(reads(api, path(CLUSTER_A))).toBe(1);
    expect(reads(api, path(CLUSTER_B))).toBe(1);
  });
});
