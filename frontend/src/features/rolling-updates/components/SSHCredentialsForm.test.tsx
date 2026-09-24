import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/test-utils";
import { listOf, stubApi } from "@/test/fetch-stub";
import type { SSHCredential, SSHKnownHost } from "@/types/api";
import { SSHCredentialsForm } from "./SSHCredentialsForm";

const CLUSTER = "c1";
const BASE = `/api/v1/clusters/${CLUSTER}`;

const creds: SSHCredential = {
  cluster_id: CLUSTER,
  username: "root",
  port: 22,
  auth_type: "password",
  has_key: false,
  created_at: "2026-09-01T00:00:00Z",
  updated_at: "2026-09-01T00:00:00Z",
};

function knownHost(id: string, host: string, fp: string): SSHKnownHost {
  return {
    id,
    cluster_id: CLUSTER,
    host,
    port: 22,
    fingerprint: fp,
    pinned_at: "2026-09-01T00:00:00Z",
  };
}

const hosts = [
  knownHost("kh-1", "192.0.2.11", "SHA256:AAAABBBBCCCCDDDD1111"),
  knownHost("kh-2", "192.0.2.12", "SHA256:AAAABBBBCCCCDDDD2222"),
];

let api: ReturnType<typeof stubApi>;

beforeEach(() => {
  api = stubApi({
    [`${BASE}/ssh-credentials`]: creds,
    [`${BASE}/nodes`]: listOf([]),
    [`${BASE}/ssh-known-hosts`]: listOf(hosts),
  });
});

afterEach(() => {
  vi.unstubAllGlobals();
});

async function renderLoaded() {
  renderWithProviders(<SSHCredentialsForm clusterId={CLUSTER} />);
  await screen.findByText("192.0.2.12:22");
}

/** The unpin button on the pinned-key row for `host`. */
function unpinButton(host: string) {
  const row = screen.getByText(`${host}:22`).closest("div.flex");
  if (!(row instanceof HTMLElement)) throw new Error(`no row for ${host}`);
  return within(row).getByRole("button", { name: "Unpin host key" });
}

describe("SSHCredentialsForm — delete credentials", () => {
  it("asks first, naming the credential, and sends nothing yet", async () => {
    const user = userEvent.setup();
    await renderLoaded();

    await user.click(
      screen.getByRole("button", { name: "Delete SSH credentials" }),
    );

    const dialog = screen.getByRole("alertdialog");
    expect(dialog).toHaveTextContent(
      "Delete SSH credentials for root on port 22?",
    );
    expect(dialog).toHaveTextContent("cannot be recovered");
    expect(api.writes()).toEqual([]);
  });

  it("Cancel sends nothing", async () => {
    const user = userEvent.setup();
    await renderLoaded();
    await user.click(
      screen.getByRole("button", { name: "Delete SSH credentials" }),
    );

    await user.click(screen.getByRole("button", { name: "Cancel" }));

    await waitFor(() => {
      expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    });
    expect(api.writes()).toEqual([]);
  });

  it("confirming sends exactly one DELETE of the credential", async () => {
    const user = userEvent.setup();
    await renderLoaded();
    await user.click(
      screen.getByRole("button", { name: "Delete SSH credentials" }),
    );

    await user.click(
      within(screen.getByRole("alertdialog")).getByRole("button", {
        name: "Delete",
      }),
    );

    await waitFor(() => {
      expect(api.writes()).toEqual([`DELETE ${BASE}/ssh-credentials`]);
    });
    // Once: a second request arriving after the first would land here.
    await new Promise((r) => setTimeout(r, 100));
    expect(api.writes()).toEqual([`DELETE ${BASE}/ssh-credentials`]);
  });
});

describe("SSHCredentialsForm — unpin host key", () => {
  it("asks first, naming the host, says it fails closed, and sends nothing yet", async () => {
    const user = userEvent.setup();
    await renderLoaded();

    await user.click(unpinButton("192.0.2.12"));

    const dialog = screen.getByRole("alertdialog");
    expect(dialog).toHaveTextContent("Unpin the host key for 192.0.2.12:22?");
    expect(dialog).toHaveTextContent("SHA256:AAAABBBBCCCCDDDD2222");
    expect(dialog).toHaveTextContent(
      "It will not trust whatever key this host presents next",
    );
    expect(dialog).toHaveTextContent(
      "SSH connections to 192.0.2.12:22 are refused",
    );
    expect(api.writes()).toEqual([]);
  });

  it("Cancel sends nothing", async () => {
    const user = userEvent.setup();
    await renderLoaded();
    await user.click(unpinButton("192.0.2.12"));

    await user.click(screen.getByRole("button", { name: "Cancel" }));

    await waitFor(() => {
      expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    });
    expect(api.writes()).toEqual([]);
  });

  it("confirming unpins exactly the row clicked, once", async () => {
    const user = userEvent.setup();
    await renderLoaded();
    await user.click(unpinButton("192.0.2.12"));

    await user.click(screen.getByRole("button", { name: "Unpin" }));

    await waitFor(() => {
      expect(api.writes()).toEqual([`DELETE ${BASE}/ssh-known-hosts/kh-2`]);
    });
    // Once: a second request arriving after the first would land here.
    await new Promise((r) => setTimeout(r, 100));
    expect(api.writes()).toEqual([`DELETE ${BASE}/ssh-known-hosts/kh-2`]);
  });
});
