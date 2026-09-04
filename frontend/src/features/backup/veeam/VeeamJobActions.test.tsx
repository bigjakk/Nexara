import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/test-utils";
import { useAuthStore } from "@/stores/auth-store";
import { VeeamJobActions } from "./VeeamJobActions";
import { VeeamSessionActions } from "./VeeamSessionActions";
import { job, runningSession } from "./veeam.fixtures";

/** The requests the component actually issued, as "METHOD path". */
let calls: string[] = [];

function stubFetch(body: unknown = { started: true }) {
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      // apiClient always passes a string path; Request and URL are only in
      // the signature because that is what fetch accepts. Each is narrowed
      // rather than stringified — a Request has no useful toString.
      const url =
        typeof input === "string"
          ? input
          : input instanceof URL
            ? input.href
            : input.url;
      calls.push(`${init?.method ?? "GET"} ${url}`);
      return Promise.resolve(
        new Response(JSON.stringify(body), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      );
    }),
  );
}

function setPermissions(permissions: string[]) {
  useAuthStore.setState({
    user: {
      id: "u1",
      email: "u@example.test",
      display_name: "Test User",
      role: "user",
    },
    permissions,
    isAuthenticated: true,
    isInitialized: true,
  });
}

beforeEach(() => {
  calls = [];
  stubFetch();
  setPermissions(["view:veeam", "execute:veeam"]);
});

afterEach(() => {
  vi.unstubAllGlobals();
  useAuthStore.setState({
    user: null,
    permissions: [],
    isAuthenticated: false,
  });
});

describe("VeeamJobActions", () => {
  it("renders nothing without execute:veeam", () => {
    setPermissions(["view:veeam", "manage:veeam"]);
    const { container } = renderWithProviders(
      <VeeamJobActions serverId="srv-1" job={job()} />,
    );
    expect(container).toBeEmptyDOMElement();
  });

  it("runs a job without a confirmation — starting one is additive", async () => {
    const user = userEvent.setup();
    renderWithProviders(<VeeamJobActions serverId="srv-1" job={job()} />);

    await user.click(screen.getByRole("button", { name: /run job now/i }));

    await waitFor(() => {
      expect(calls).toContain(
        "POST /api/v1/veeam-servers/srv-1/jobs/3953c24f-bbe6-41fc-ae2f-a34e25bfd614/start",
      );
    });
  });

  // 204: Veeam accepted the request but the job had nothing to process, so no
  // run exists. Without this the operator watches for a session that is never
  // going to appear and concludes the click was dropped.
  it("says so when a start creates no run at all", async () => {
    stubFetch({ started: false, message: "no objects to process" });
    const user = userEvent.setup();
    renderWithProviders(<VeeamJobActions serverId="srv-1" job={job()} />);

    await user.click(screen.getByRole("button", { name: /run job now/i }));

    expect(
      await screen.findByText(/no objects to process/i),
    ).toBeInTheDocument();
  });

  // Both disruptive actions confirm, per the project's standing rule. The
  // assertion that matters is that NOTHING is sent until the operator agrees.
  it.each([
    {
      name: "stop",
      job: job({ status: "Running" }),
      open: /stop job/i,
      confirmLabel: "Stop job",
      path: "stop",
    },
    {
      name: "disable",
      job: job(),
      open: /disable job/i,
      confirmLabel: "Disable job",
      path: "disable",
    },
  ])(
    "confirms before $name",
    async ({ job: target, open, confirmLabel, path }) => {
      const user = userEvent.setup();
      renderWithProviders(<VeeamJobActions serverId="srv-1" job={target} />);

      await user.click(screen.getByRole("button", { name: open }));
      expect(calls).toHaveLength(0);

      await user.click(screen.getByRole("button", { name: confirmLabel }));
      await waitFor(() => {
        expect(calls).toContain(
          `POST /api/v1/veeam-servers/srv-1/jobs/3953c24f-bbe6-41fc-ae2f-a34e25bfd614/${path}`,
        );
      });
    },
  );

  // Re-enabling a job restores protection; refusing to do it until someone
  // clicks twice would be confirmation fatigue with nothing to protect.
  it("enables a disabled job without a confirmation", async () => {
    const user = userEvent.setup();
    renderWithProviders(
      <VeeamJobActions serverId="srv-1" job={job({ status: "Disabled" })} />,
    );

    await user.click(screen.getByRole("button", { name: /enable job/i }));
    await waitFor(() => {
      expect(calls).toContain(
        "POST /api/v1/veeam-servers/srv-1/jobs/3953c24f-bbe6-41fc-ae2f-a34e25bfd614/enable",
      );
    });
  });

  // The defect the live lab test exposed. veeam_jobs.status is refreshed by
  // the inventory pass every few minutes, so a job started from Nexara still
  // reads "Stopped" long after its run exists — and Stop, the button that
  // undoes what the operator just did, was the one missing.
  it("offers Stop on a live run even while the stored status is stale", async () => {
    const user = userEvent.setup();
    renderWithProviders(
      <VeeamJobActions
        serverId="srv-1"
        job={job({
          status: "Stopped",
          running_session_id: "20ff3c43-65c0-414d-ae03-10cf59fd2faa",
          running_session_state: "Working",
        })}
      />,
    );

    expect(
      screen.queryByRole("button", { name: /run job now/i }),
    ).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: /stop job/i }));
    expect(calls).toHaveLength(0);
    await user.click(screen.getByRole("button", { name: "Stop job" }));
    await waitFor(() => {
      expect(calls).toContain(
        "POST /api/v1/veeam-servers/srv-1/jobs/3953c24f-bbe6-41fc-ae2f-a34e25bfd614/stop",
      );
    });
  });

  it("offers no run button while a job is already starting", () => {
    renderWithProviders(
      <VeeamJobActions serverId="srv-1" job={job({ status: "Starting" })} />,
    );
    expect(
      screen.queryByRole("button", { name: /run job now/i }),
    ).not.toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /stop job/i }),
    ).toBeInTheDocument();
  });
});

describe("VeeamSessionActions", () => {
  it("renders nothing without execute:veeam", () => {
    // The positive control comes first on purpose. A terminal run hides the
    // stop button on its own, so asserting only the empty case would pass for
    // the wrong reason the moment someone hands this a finished session.
    const running = runningSession();
    renderWithProviders(
      <VeeamSessionActions serverId="srv-1" session={running} />,
    );
    expect(
      screen.getByRole("button", { name: /stop run/i }),
    ).toBeInTheDocument();

    setPermissions(["view:veeam"]);
    const { container } = renderWithProviders(
      <VeeamSessionActions serverId="srv-1" session={running} />,
    );
    expect(container).toBeEmptyDOMElement();
  });

  // ESessionState has twelve values and only "Stopped" is terminal. A run
  // parked on WaitingRepository is exactly the one an operator wants to kill,
  // so an allow-list of the obviously-running states would hide the button
  // where it is needed most.
  it.each(["Working", "WaitingRepository", "Idle", "ActionRequired"])(
    "offers a stop for a run in state %s",
    (state) => {
      renderWithProviders(
        <VeeamSessionActions
          serverId="srv-1"
          session={runningSession({ state })}
        />,
      );
      expect(
        screen.getByRole("button", { name: /stop run/i }),
      ).toBeInTheDocument();
    },
  );

  it.each(["Stopped", "Stopping", ""])(
    "offers no stop for a run in state %s",
    (state) => {
      const { container } = renderWithProviders(
        <VeeamSessionActions
          serverId="srv-1"
          session={runningSession({ state })}
        />,
      );
      expect(container).toBeEmptyDOMElement();
    },
  );

  it("confirms before stopping a run", async () => {
    const user = userEvent.setup();
    renderWithProviders(
      <VeeamSessionActions serverId="srv-1" session={runningSession()} />,
    );

    await user.click(screen.getByRole("button", { name: /stop run/i }));
    expect(calls).toHaveLength(0);

    await user.click(screen.getByRole("button", { name: "Stop run" }));
    await waitFor(() => {
      expect(calls).toContain(
        "POST /api/v1/veeam-servers/srv-1/sessions/20ff3c43-65c0-414d-ae03-10cf59fd2faa/stop",
      );
    });
  });
});
