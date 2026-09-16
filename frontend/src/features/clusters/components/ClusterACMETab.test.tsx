import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";

import { ClusterACMETab } from "./ClusterACMETab";
import { ApiClientError } from "@/lib/api-client";
import type { NodeACMEConfig } from "@/features/acme/api/acme-queries";

const CLUSTER = "cccccccc-0000-0000-0000-000000000002";
const NODE = "pve-01";
const NODES_PATH = `/api/v1/clusters/${CLUSTER}/nodes`;
const CONFIG_PATH = `/api/v1/clusters/${CLUSTER}/nodes/${NODE}/acme-config`;

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

vi.mock("@/hooks/useAuth", () => ({
  useAuth: () => ({ canManage: () => true }),
}));

function renderTab() {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  );
  return { qc, ...render(<ClusterACMETab clusterId={CLUSTER} />, { wrapper }) };
}

/** Opens the edit dialog on the nth configured domain, defaulting to the first. */
async function openEdit(user: ReturnType<typeof userEvent.setup>, index = 0) {
  const rows = await screen.findAllByRole("button", { name: "Edit" });
  const row = rows[index];
  if (!row) throw new Error(`no Edit button at index ${String(index)}`);
  await user.click(row);
}

function configGets() {
  return getMock.mock.calls.filter((c) => c[0] === CONFIG_PATH);
}

/**
 * Serves the node list plus a sequence of acme-config reads, one per GET. The
 * last entry repeats, so a test that only cares about the first read passes a
 * single config.
 */
function serve(configs: NodeACMEConfig[]) {
  listMock.mockImplementation((path: string) =>
    path === NODES_PATH
      ? Promise.resolve([{ name: NODE, node_name: NODE }])
      : Promise.resolve([]),
  );
  let call = 0;
  getMock.mockImplementation((path: string) => {
    if (path !== CONFIG_PATH) return Promise.resolve(null);
    const cfg = configs[Math.min(call, configs.length - 1)];
    call += 1;
    return Promise.resolve(cfg);
  });
}

/** Opens the Node Certificates tab and waits for the first config read. */
async function openCertificatesTab(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByRole("tab", { name: "Node Certificates" }));
  await waitFor(() => {
    expect(getMock).toHaveBeenCalledWith(CONFIG_PATH);
  });
}

async function typeDomainAndSave(
  user: ReturnType<typeof userEvent.setup>,
  domain: string,
) {
  await user.type(screen.getByPlaceholderText("node1.example.com"), domain);
  await user.click(screen.getByRole("button", { name: "Save" }));
}

beforeEach(() => {
  listMock.mockReset();
  getMock.mockReset();
  putMock.mockReset();
  putMock.mockResolvedValue({ status: "ok" });
});

describe("ClusterACMETab domain save", () => {
  it("sends the config digest and never the cached ACME account", async () => {
    const user = userEvent.setup();
    serve([{ acme: "account=acme-account-01", digest: "d1" }]);
    renderTab();
    await openCertificatesTab(user);

    await user.click(screen.getByRole("button", { name: /Add Domain/ }));
    await typeDomainAndSave(user, "node1.example.com");

    await waitFor(() => {
      expect(putMock).toHaveBeenCalledTimes(1);
    });
    const body = putMock.mock.calls[0]?.[1] as NodeACMEConfig;
    expect(body.digest).toBe("d1");
    expect(body.acmedomain0).toBe("domain=node1.example.com");
    // The account rode along on every domain save and wrote the cached copy
    // back over whatever Proxmox actually had. It must not be in the payload.
    expect(body).not.toHaveProperty("acme");
  });

  it("resolves an added domain's slot against the config the digest came from", async () => {
    const user = userEvent.setup();
    // The read behind the open dialog shows slot 1 free; the refetch that
    // opening the dialog triggers shows another operator has taken it. The
    // save must land on slot 2, not overwrite them with a digest that now
    // matches.
    serve([
      { acmedomain0: "node1.example.com", digest: "d1" },
      {
        acmedomain0: "node1.example.com",
        acmedomain1: "node2.example.com",
        digest: "d2",
      },
    ]);
    renderTab();
    await openCertificatesTab(user);

    await user.click(screen.getByRole("button", { name: /Add Domain/ }));
    expect(await screen.findByText("node2.example.com")).toBeInTheDocument();
    await typeDomainAndSave(user, "node3.example.com");

    await waitFor(() => {
      expect(putMock).toHaveBeenCalledTimes(1);
    });
    const body = putMock.mock.calls[0]?.[1] as NodeACMEConfig;
    expect(body.digest).toBe("d2");
    expect(body.acmedomain2).toBe("domain=node3.example.com");
    expect(body).not.toHaveProperty("acmedomain1");
  });

  it("keeps an edited domain's own digest instead of refreshing it underneath", async () => {
    const user = userEvent.setup();
    // Only an add refetches on open. An edit's fields come from the config on
    // screen, so pulling a newer digest under them would let the save carry a
    // digest matching a version of this slot the operator never saw — the
    // compare-and-swap would pass and overwrite it.
    serve([
      { acmedomain0: "domain=node1.example.com", digest: "d1" },
      { acmedomain0: "domain=node2.example.com", digest: "d2" },
    ]);
    renderTab();
    await openCertificatesTab(user);

    expect(await screen.findByText("node1.example.com")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Edit" }));
    // Checked before the save: afterwards the mutation's own invalidation
    // refetches the config legitimately, which would mask an open-time one.
    expect(getMock.mock.calls.filter((c) => c[0] === CONFIG_PATH)).toHaveLength(
      1,
    );

    await user.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => {
      expect(putMock).toHaveBeenCalledTimes(1);
    });
    const body = putMock.mock.calls[0]?.[1] as NodeACMEConfig;
    expect(body.digest).toBe("d1");
    expect(body.acmedomain0).toBe("domain=node1.example.com");
  });

  it("refuses to save while the node config is unread", async () => {
    const user = userEvent.setup();
    listMock.mockImplementation((path: string) =>
      path === NODES_PATH
        ? Promise.resolve([{ name: NODE, node_name: NODE }])
        : Promise.resolve([]),
    );
    // Unread, the free-slot scan answers 0 and no digest is attached, so the
    // write would blindly overwrite acmedomain0 with the check switched off.
    getMock.mockImplementation((path: string) =>
      path === CONFIG_PATH
        ? Promise.reject(new ApiClientError(502, { error: "bad", message: "" }))
        : Promise.resolve(null),
    );
    renderTab();
    await openCertificatesTab(user);

    await user.click(screen.getByRole("button", { name: /Add Domain/ }));
    await user.type(
      screen.getByPlaceholderText("node1.example.com"),
      "node1.example.com",
    );

    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(putMock).not.toHaveBeenCalled();
  });

  it("reports a dropped connection rather than failing silently", async () => {
    const user = userEvent.setup();
    serve([{ digest: "d1" }]);
    // How fetch rejects when the connection drops. describeError returns ""
    // for a TypeError and the hook has opted out of the global toast, so
    // without a fallback this failure renders as nothing on either surface.
    putMock.mockRejectedValueOnce(new TypeError("Failed to fetch"));
    renderTab();
    await openCertificatesTab(user);

    await user.click(screen.getByRole("button", { name: /Add Domain/ }));
    await typeDomainAndSave(user, "node1.example.com");

    expect(
      await screen.findByText(/check your connection and try again/),
    ).toBeInTheDocument();
  });

  it("keeps an edit's pinned digest when the config refetches underneath it", async () => {
    const user = userEvent.setup();
    // Removing the refetch from openEditDomain removed one trigger, not the
    // class: a WebSocket reconnect invalidates every active query, and so does
    // ordering or renewing a certificate. Read live at save time, the digest
    // would move to d2 while the form still showed the d1 values — and the
    // compare-and-swap would pass and destroy the change that produced d2.
    serve([
      { acmedomain0: "domain=node1.example.com", digest: "d1" },
      { acmedomain0: "domain=node2.example.com", digest: "d2" },
    ]);
    const { qc } = renderTab();
    await openCertificatesTab(user);
    await openEdit(user);

    await qc.invalidateQueries();
    await waitFor(() => {
      expect(configGets()).toHaveLength(2);
    });
    expect(await screen.findByText("node2.example.com")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => {
      expect(putMock).toHaveBeenCalledTimes(1);
    });
    const body = putMock.mock.calls[0]?.[1] as NodeACMEConfig;
    expect(body.digest).toBe("d1");
  });

  it("still saves an edit after a background refresh fails", async () => {
    const user = userEvent.setup();
    // A failed background refetch flips the query to "error" while keeping the
    // data, so gating Save on isSuccess would strand every edit on a node
    // whose six slots are full — the Add button, and so the only other
    // refetch, is hidden at six. An edit needs nothing from that read anyway.
    listMock.mockImplementation((path: string) =>
      path === NODES_PATH
        ? Promise.resolve([{ name: NODE, node_name: NODE }])
        : Promise.resolve([]),
    );
    let call = 0;
    getMock.mockImplementation((path: string) => {
      if (path !== CONFIG_PATH) return Promise.resolve(null);
      call += 1;
      return call === 1
        ? Promise.resolve({
            acmedomain0: "domain=node1.example.com",
            digest: "d1",
          })
        : Promise.reject(
            new ApiClientError(502, { error: "bad", message: "" }),
          );
    });
    const { qc } = renderTab();
    await openCertificatesTab(user);
    await openEdit(user);

    await qc.invalidateQueries();
    await waitFor(() => {
      expect(configGets()).toHaveLength(2);
    });

    expect(screen.getByRole("button", { name: "Save" })).toBeEnabled();
    await user.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => {
      expect(putMock).toHaveBeenCalledTimes(1);
    });
    expect((putMock.mock.calls[0]?.[1] as NodeACMEConfig).digest).toBe("d1");
  });

  it("re-pins an edit's digest after a conflict so the retry can go through", async () => {
    const user = userEvent.setup();
    // A conflict the operator has been shown is the one thing allowed to move
    // a pinned digest. Without that, an edit could never be retried: every
    // attempt would re-send the digest it already lost on.
    serve([
      { acmedomain0: "domain=node1.example.com", digest: "d1" },
      { acmedomain0: "domain=node1.example.com", digest: "d2" },
    ]);
    putMock.mockRejectedValueOnce(
      new ApiClientError(409, { error: "conflict", message: "changed" }),
    );
    renderTab();
    await openCertificatesTab(user);
    await openEdit(user);

    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(await screen.findByText("changed")).toBeInTheDocument();
    await waitFor(() => {
      expect(configGets()).toHaveLength(2);
    });

    await user.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => {
      expect(putMock).toHaveBeenCalledTimes(2);
    });
    expect((putMock.mock.calls[1]?.[1] as NodeACMEConfig).digest).toBe("d2");
  });

  it("holds the pin when the conflict refetch itself fails", async () => {
    const user = userEvent.setup();
    // A failed refetch RETAINS the last successful data, so re-pinning on
    // `res.data` alone is a guard that can never fail: it would move the pin
    // to d2 — a change this dialog never saw — and the retry would overwrite
    // it. The pin must only move on a refetch that actually succeeded.
    listMock.mockImplementation((path: string) =>
      path === NODES_PATH
        ? Promise.resolve([{ name: NODE, node_name: NODE }])
        : Promise.resolve([]),
    );
    let call = 0;
    getMock.mockImplementation((path: string) => {
      if (path !== CONFIG_PATH) return Promise.resolve(null);
      call += 1;
      if (call === 1)
        return Promise.resolve({
          acmedomain0: "domain=node1.example.com",
          digest: "d1",
        });
      if (call === 2)
        return Promise.resolve({
          acmedomain0: "domain=node2.example.com",
          digest: "d2",
        });
      return Promise.reject(
        new ApiClientError(502, { error: "bad", message: "" }),
      );
    });
    putMock.mockRejectedValueOnce(
      new ApiClientError(409, { error: "conflict", message: "changed" }),
    );
    const { qc } = renderTab();
    await openCertificatesTab(user);
    await openEdit(user);

    // The cache moves to d2 behind the open dialog, which still shows d1's
    // values; the conflict refetch that follows cannot replace it.
    await qc.invalidateQueries();
    await waitFor(() => {
      expect(configGets()).toHaveLength(2);
    });

    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(await screen.findByText("changed")).toBeInTheDocument();
    await waitFor(() => {
      expect(configGets()).toHaveLength(3);
    });

    await user.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => {
      expect(putMock).toHaveBeenCalledTimes(2);
    });
    expect((putMock.mock.calls[1]?.[1] as NodeACMEConfig).digest).toBe("d1");
  });

  it("does not let one dialog's conflict refetch re-pin the next dialog", async () => {
    const user = userEvent.setup();
    // The conflict refetch is async and nothing about it is tied to the dialog
    // that fired it. Cancelling and opening another row while it is in flight
    // must not land its digest on a pin that was just set to match different
    // values — that would be the same overwrite, one dialog removed.
    listMock.mockImplementation((path: string) =>
      path === NODES_PATH
        ? Promise.resolve([{ name: NODE, node_name: NODE }])
        : Promise.resolve([]),
    );
    let releaseRefetch: (() => void) | undefined;
    let call = 0;
    const firstRead = {
      acmedomain0: "domain=node1.example.com",
      acmedomain1: "domain=node2.example.com",
      digest: "d1",
    };
    getMock.mockImplementation((path: string) => {
      if (path !== CONFIG_PATH) return Promise.resolve(null);
      call += 1;
      if (call === 1) return Promise.resolve(firstRead);
      return new Promise((resolve) => {
        releaseRefetch = () => {
          resolve({ ...firstRead, digest: "d3" });
        };
      });
    });
    putMock.mockRejectedValueOnce(
      new ApiClientError(409, { error: "conflict", message: "changed" }),
    );
    renderTab();
    await openCertificatesTab(user);

    // First dialog: slot 0. Save conflicts and leaves a refetch hanging.
    await openEdit(user);
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(await screen.findByText("changed")).toBeInTheDocument();
    await waitFor(() => {
      expect(configGets()).toHaveLength(2);
    });

    // Second dialog: slot 1, pinned to the read still on screen.
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    await openEdit(user, 1);
    releaseRefetch?.();
    await waitFor(() => {
      expect(screen.getByRole("button", { name: "Save" })).toBeEnabled();
    });

    await user.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => {
      expect(putMock).toHaveBeenCalledTimes(2);
    });
    const body = putMock.mock.calls[1]?.[1] as NodeACMEConfig;
    expect(body.acmedomain1).toBe("domain=node2.example.com");
    expect(body.digest).toBe("d1");
  });

  it("reports a digest conflict in the dialog and refetches so a retry works", async () => {
    const user = userEvent.setup();
    const conflict =
      "The node's configuration changed since it was read — reload and try again.";
    serve([{ digest: "d1" }, { digest: "d2" }, { digest: "d3" }]);
    putMock.mockRejectedValueOnce(
      new ApiClientError(409, { error: "conflict", message: conflict }),
    );
    renderTab();
    await openCertificatesTab(user);

    await user.click(screen.getByRole("button", { name: /Add Domain/ }));
    await typeDomainAndSave(user, "node1.example.com");

    // The dialog stays open carrying the typed value, so the operator can
    // retry deliberately rather than having the retry made on their behalf.
    expect(await screen.findByText(conflict)).toBeInTheDocument();
    expect(screen.getByPlaceholderText("node1.example.com")).toHaveValue(
      "node1.example.com",
    );
    expect(putMock).toHaveBeenCalledTimes(1);

    // The conflict refetch reloads the digest, so pressing Save again sends
    // the current one instead of repeating the doomed write.
    await waitFor(() => {
      expect(
        getMock.mock.calls.filter((c) => c[0] === CONFIG_PATH),
      ).toHaveLength(3);
    });
    await user.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => {
      expect(putMock).toHaveBeenCalledTimes(2);
    });
    expect((putMock.mock.calls[1]?.[1] as NodeACMEConfig).digest).toBe("d3");
  });
});
