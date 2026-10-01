import { beforeEach, describe, expect, it, vi } from "vitest";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClientProvider } from "@tanstack/react-query";
import { toast } from "sonner";

import { apiClient, ApiClientError } from "@/lib/api-client";
import { createAppQueryClient } from "@/test/app-query-client";
import type { NodeNotes } from "../../api/node-options-queries";
import { NodeNotesCard } from "./NodeNotesCard";

/**
 * The Notes card shows a node's notes as plain text and edits them in a dialog
 * that is mounted afresh for each open, as the Options card's is
 * (NodeOptionsCard.test.tsx has the cases the two share: the digest pin, the
 * re-read after a 409, focus). What is particular to notes:
 *
 *   - They are read from a route of their own, which answers only to users who
 *     can manage the node, so anyone else's card asks for nothing and says who
 *     can read them. They are written through the options route.
 *   - They are free text written by anyone who can edit the node, so they are
 *     never read as markup.
 *   - Proxmox gives every line a trailing newline that is not part of what
 *     anyone wrote, and emptying them removes them.
 *   - After a 409 the notes now stored are shown in the dialog, since saving
 *     replaces them and the card that shows them is behind the overlay.
 */

vi.mock("@/lib/api-client", async () => {
  const actual =
    await vi.importActual<typeof import("@/lib/api-client")>(
      "@/lib/api-client",
    );
  return {
    ...actual,
    apiClient: {
      get: vi.fn(),
      list: vi.fn(),
      post: vi.fn(),
      put: vi.fn(),
      delete: vi.fn(),
    },
  };
});

vi.mock("sonner", () => ({
  toast: { success: vi.fn(), warning: vi.fn(), error: vi.fn() },
}));

const mockedGet = vi.mocked(apiClient.get);
const mockedPut = vi.mocked(apiClient.put);
const mockedToastError = vi.mocked(toast.error);

const CLUSTER = "cccccccc-0000-0000-0000-000000000008";
const NODE = "pve-01";
// Read from the notes route, written through the options one.
const NOTES_URL = `/api/v1/clusters/${CLUSTER}/nodes/${NODE}/notes`;
const OPTIONS_URL = `/api/v1/clusters/${CLUSTER}/nodes/${NODE}/options`;
const NOTES_KEY = ["clusters", CLUSTER, "nodes", NODE, "notes"];
const DIALOG = `Edit Notes - ${NODE}`;
const STALE =
  "The node's configuration changed since it was read — reload and try again.";
const CHANGED = "This node's configuration changed while this dialog was open.";
const VIEWERS = "Notes are visible to users who can manage this node.";

type UserEvent = ReturnType<typeof userEvent.setup>;

function deferred<T>() {
  let resolve: (value: T) => void = () => undefined;
  let reject: (reason: unknown) => void = () => undefined;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

async function flush(): Promise<void> {
  await act(async () => {
    await new Promise<void>((resolve) => {
      setTimeout(resolve, 0);
    });
  });
}

/** One read per GET, the last repeating. */
function serve(...reads: NodeNotes[]) {
  let call = 0;
  mockedGet.mockImplementation((path: string) => {
    if (path !== NOTES_URL) {
      return Promise.reject(new Error(`unexpected GET ${path}`));
    }
    const next = reads[Math.min(call, reads.length - 1)];
    call += 1;
    return Promise.resolve(next);
  });
}

function renderCard(over: { online?: boolean; canEdit?: boolean } = {}) {
  const qc = createAppQueryClient();
  const ui = (online: boolean, canEdit: boolean) => (
    <QueryClientProvider client={qc}>
      <NodeNotesCard
        clusterId={CLUSTER}
        nodeName={NODE}
        online={online}
        canEdit={canEdit}
      />
    </QueryClientProvider>
  );
  const view = render(ui(over.online ?? true, over.canEdit ?? true));
  return {
    qc,
    goOffline: () => {
      view.rerender(ui(false, over.canEdit ?? true));
    },
    /** The user's permissions changing under the card, as a refresh brings them. */
    grantManage: () => {
      view.rerender(ui(over.online ?? true, true));
    },
  };
}

async function openDialog(user: UserEvent): Promise<HTMLElement> {
  await user.click(
    await screen.findByRole("button", { name: "Edit node notes" }),
  );
  return screen.findByRole("dialog", { name: DIALOG });
}

function notesField(dialog: HTMLElement): HTMLTextAreaElement {
  return within(dialog).getByLabelText<HTMLTextAreaElement>("Notes");
}

function saveButton(dialog: HTMLElement): HTMLElement {
  return within(dialog).getByRole("button", { name: /^Save/ });
}

/** The URLs the card has asked for with a GET. */
function readUrls(): unknown[] {
  return mockedGet.mock.calls.map(([path]) => path);
}

beforeEach(() => {
  mockedGet.mockReset();
  mockedPut.mockReset();
  mockedToastError.mockReset();
  mockedPut.mockResolvedValue({ status: "ok" });
});

describe("what the card shows", () => {
  it("shows the notes as text, line breaks kept", async () => {
    serve({ description: "line one\nline two\n", digest: "d1" });
    renderCard();

    const notes = await screen.findByText(
      (_, el) => el?.textContent === "line one\nline two",
    );
    expect(notes).toHaveClass("whitespace-pre-wrap");
    expect(notes).toHaveAttribute("tabindex", "0");
  });

  it("never reads the notes as markup, whatever they hold", async () => {
    const hostile =
      '<b>x</b> <img src=x onerror="alert(1)"> [link](https://example.com) **bold**';
    serve({ description: `${hostile}\n`, digest: "d1" });
    renderCard();

    const notes = await screen.findByText(hostile);
    // The text of the tags, not the tags.
    expect(notes.children).toHaveLength(0);
    expect(document.querySelector("b, img, a")).toBeNull();
  });

  it.each([
    ["unset", { digest: "d1" }],
    ["empty", { description: "", digest: "d1" }],
    ["only a newline", { description: "\n", digest: "d1" }],
    ["only whitespace", { description: "  \n \n", digest: "d1" }],
  ])("says there are none when they are %s", async (_, options) => {
    serve(options);
    renderCard();

    expect(await screen.findByText("No notes.")).toBeInTheDocument();
  });

  it("does not read an offline node, and says why", async () => {
    serve({ description: "sentinel notes\n", digest: "d1" });
    renderCard({ online: false });

    expect(
      await screen.findByText(
        "Notes can only be read while the node is online.",
      ),
    ).toBeInTheDocument();
    await flush();
    expect(mockedGet).not.toHaveBeenCalled();
    expect(
      screen.queryByRole("button", { name: "Edit node notes" }),
    ).toBeNull();
  });

  it("reads the notes route, and no other", async () => {
    serve({ description: "sentinel notes\n", digest: "d1" });
    renderCard();

    expect(await screen.findByText("sentinel notes")).toBeInTheDocument();
    expect(readUrls()).toEqual([NOTES_URL]);
  });

  it("is read under the node's own prefix, which an ACME save invalidates", async () => {
    serve({ description: "sentinel notes\n", digest: "d1" });
    const { qc } = renderCard();
    await screen.findByText("sentinel notes");
    expect(qc.getQueryData(NOTES_KEY)).toEqual({
      description: "sentinel notes\n",
      digest: "d1",
    });

    // useSetNodeACMEConfig invalidates ["clusters", id, "nodes", node] after a
    // save: the file's digest moved, and the notes read holds it.
    await act(async () => {
      await qc.invalidateQueries({
        queryKey: ["clusters", CLUSTER, "nodes", NODE],
      });
    });

    expect(readUrls()).toEqual([NOTES_URL, NOTES_URL]);
  });

  it("asks for nothing from someone who cannot manage nodes, and says who can read the notes", async () => {
    serve({ description: "sentinel notes\n", digest: "d1" });
    renderCard({ canEdit: false });

    expect(await screen.findByText(VIEWERS)).toBeInTheDocument();
    await flush();
    // The route would answer 403: not a request to make, and not a failure to
    // show.
    expect(mockedGet).not.toHaveBeenCalled();
    expect(screen.queryByText("sentinel notes")).toBeNull();
    expect(screen.queryByText("No notes.")).toBeNull();
    expect(screen.queryByText(/Could not load/)).toBeNull();
    expect(
      screen.queryByRole("button", { name: "Edit node notes" }),
    ).toBeNull();
  });

  it("says the same of an offline node, to someone who cannot manage nodes", async () => {
    serve({ description: "sentinel notes\n", digest: "d1" });
    renderCard({ canEdit: false, online: false });

    expect(await screen.findByText(VIEWERS)).toBeInTheDocument();
    expect(
      screen.queryByText("Notes can only be read while the node is online."),
    ).toBeNull();
    expect(mockedGet).not.toHaveBeenCalled();
  });

  it("starts reading the notes once the user is allowed to", async () => {
    serve({ description: "sentinel notes\n", digest: "d1" });
    const { grantManage } = renderCard({ canEdit: false });
    expect(await screen.findByText(VIEWERS)).toBeInTheDocument();
    await flush();
    expect(mockedGet).not.toHaveBeenCalled();

    // A refreshed token carries the permission, and the card is the same one.
    grantManage();

    expect(await screen.findByText("sentinel notes")).toBeInTheDocument();
    expect(screen.queryByText(VIEWERS)).toBeNull();
    expect(readUrls()).toEqual([NOTES_URL]);
    expect(
      await screen.findByRole("button", { name: "Edit node notes" }),
    ).toBeInTheDocument();
  });

  it("offers a retry for a read that failed, and no Edit", async () => {
    mockedGet.mockRejectedValueOnce(
      new ApiClientError(502, {
        error: "bad_gateway",
        message: "Failed to connect to Proxmox",
      }),
    );
    renderCard();

    expect(
      await screen.findByText("Could not load this node's notes."),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Retry" })).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Edit node notes" }),
    ).toBeNull();
  });
});

// A 403 on the notes read withdraws what the card had read, its notes and its
// Edit. What it then says depends on who refused:
//
//   - Nexara's own permission check. canManage is a flat check (it names no
//     cluster) and its cache can be a refresh behind the server's, so a user the
//     card takes for a manager can be refused: the viewer's state, no Retry.
//   - Anything else that answers 403, Proxmox's in practice (mapProxmoxError
//     passes it through as a 403). It says nothing about the user, and its
//     message says what to fix: a failure like any other, with a Retry.
describe("a manager the server refuses", () => {
  // What the notes route refuses a caller who cannot manage the node with. It is
  // declared clusterCheck("manage", "node"), whose RequireClusterPermission
  // answers through requireClusterPerm (internal/api/handlers/permission.go)
  // with exactly this text: the backend pins it as rbacDeniedMessage
  // (internal/api/registry_route_sweep_test.go).
  const INSUFFICIENT = "Insufficient permissions";
  // What a route declared with Alternatives is refused with instead
  // (RequireAnyPermission, internal/api/handlers/permission_middleware.go). The
  // notes route is not one; the card must not lose the viewer's state if it is.
  const REQUIRES = "Requires one of: manage:node";
  // mapProxmoxError, internal/api/handlers/proxmox_error.go: a Proxmox 403,
  // passed on as one.
  const PROXMOX_DENIED = "Proxmox API permission denied";

  function refusedByNexara(message = INSUFFICIENT): ApiClientError {
    return new ApiClientError(403, { error: "forbidden", message });
  }

  function refusedByProxmox(message = PROXMOX_DENIED): ApiClientError {
    return new ApiClientError(403, { error: "forbidden", message });
  }

  /** Reads the notes, then has `refetch` refuse the next read. */
  async function readThenRefused(refusal: ApiClientError) {
    serve({ description: "sentinel notes\n", digest: "d1" });
    const rendered = renderCard();
    expect(await screen.findByText("sentinel notes")).toBeInTheDocument();
    expect(
      await screen.findByRole("button", { name: "Edit node notes" }),
    ).toBeInTheDocument();

    mockedGet.mockRejectedValueOnce(refusal);
    await act(async () => {
      await rendered.qc.invalidateQueries({ queryKey: NOTES_KEY });
    });
    return rendered;
  }

  it("is shown the viewer's sentence on a first read that Nexara refuses, with no Retry and no Edit", async () => {
    mockedGet.mockRejectedValueOnce(refusedByNexara());
    renderCard();

    expect(await screen.findByText(VIEWERS)).toBeInTheDocument();
    // A retry would be refused again, and the refusal is not a failure to
    // report as one.
    expect(screen.queryByRole("button", { name: "Retry" })).toBeNull();
    expect(screen.queryByText(/Could not load/)).toBeNull();
    expect(screen.queryByText(INSUFFICIENT)).toBeNull();
    expect(
      screen.queryByRole("button", { name: "Edit node notes" }),
    ).toBeNull();
    expect(readUrls()).toEqual([NOTES_URL]);
  });

  it("is shown the same for the refusal of a route declared with Alternatives", async () => {
    mockedGet.mockRejectedValueOnce(refusedByNexara(REQUIRES));
    renderCard();

    expect(await screen.findByText(VIEWERS)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Retry" })).toBeNull();
    expect(screen.queryByText(/Could not load/)).toBeNull();
    expect(screen.queryByText(REQUIRES)).toBeNull();
    expect(
      screen.queryByRole("button", { name: "Edit node notes" }),
    ).toBeNull();
  });

  it("loses the notes it had read when a refetch is refused by Nexara, and Edit with them", async () => {
    const { qc } = await readThenRefused(refusedByNexara());

    expect(await screen.findByText(VIEWERS)).toBeInTheDocument();
    // Still in the cache, and no longer on screen: what the server now refuses
    // to give is not for this user to keep reading.
    expect(qc.getQueryData(NOTES_KEY)).toEqual({
      description: "sentinel notes\n",
      digest: "d1",
    });
    expect(screen.queryByText("sentinel notes")).toBeNull();
    expect(screen.queryByText("No notes.")).toBeNull();
    expect(
      screen.queryByRole("button", { name: "Edit node notes" }),
    ).toBeNull();
    expect(screen.queryByRole("button", { name: "Retry" })).toBeNull();
    expect(screen.queryByText(/Could not load/)).toBeNull();
  });

  // A 403 is not Nexara's because it is a 403. A Proxmox one is a failure with a
  // message that says what to fix — Nexara's own API token missing Sys.Audit on
  // /, for one, which the node's config read needs — and a manager told that
  // they may not manage the node would lose it, and the Retry with it.
  it.each([
    ["Proxmox's permission denied", "Proxmox API permission denied"],
    [
      "Proxmox's own words, passed through",
      "Proxmox API: Permission check failed (/, Sys.Audit)",
    ],
    ["a 403 with some other message", "Forbidden"],
    // Nexara's refusals are the whole message ("Insufficient permissions") or
    // start with the alternatives phrase; these only contain them.
    [
      "Nexara's words inside Proxmox's wrapping",
      "Proxmox API: Insufficient permissions",
    ],
    [
      "a Proxmox message that mentions the alternatives phrase",
      "Proxmox API: Requires one of: manage:node",
    ],
  ])(
    "shows %s on a first read as the failure it is, with its message and a Retry, and no Edit",
    async (_, message) => {
      mockedGet.mockRejectedValueOnce(refusedByProxmox(message));
      renderCard();

      expect(
        await screen.findByText("Could not load this node's notes."),
      ).toBeInTheDocument();
      expect(screen.getByText(message)).toBeInTheDocument();
      expect(screen.getByRole("button", { name: "Retry" })).toBeInTheDocument();
      expect(screen.queryByText(VIEWERS)).toBeNull();
      expect(
        screen.queryByRole("button", { name: "Edit node notes" }),
      ).toBeNull();
    },
  );

  it("withdraws the notes it had read when a refetch is refused by Proxmox, and shows why, with a Retry", async () => {
    const { qc } = await readThenRefused(refusedByProxmox());

    // The failure, and not the viewer's state: nothing here is about the user.
    expect(
      await screen.findByText("Could not load this node's notes."),
    ).toBeInTheDocument();
    expect(screen.getByText(PROXMOX_DENIED)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Retry" })).toBeInTheDocument();
    expect(screen.queryByText(VIEWERS)).toBeNull();
    // But what was read is withdrawn all the same, and Edit with it.
    expect(screen.queryByText("sentinel notes")).toBeNull();
    expect(screen.queryByText("No notes.")).toBeNull();
    expect(
      screen.queryByRole("button", { name: "Edit node notes" }),
    ).toBeNull();
    expect(qc.getQueryData(NOTES_KEY)).toEqual({
      description: "sentinel notes\n",
      digest: "d1",
    });
  });

  it("reads the notes again on that Retry, and has them back once Proxmox allows it", async () => {
    const user = userEvent.setup();
    await readThenRefused(refusedByProxmox());
    await screen.findByText(PROXMOX_DENIED);

    await user.click(screen.getByRole("button", { name: "Retry" }));

    expect(await screen.findByText("sentinel notes")).toBeInTheDocument();
    expect(screen.queryByText(PROXMOX_DENIED)).toBeNull();
    expect(
      await screen.findByRole("button", { name: "Edit node notes" }),
    ).toBeInTheDocument();
  });

  it("says the node is offline, not the failure, for a refusal that is stale by then", async () => {
    mockedGet.mockRejectedValueOnce(refusedByProxmox());
    const { goOffline } = renderCard();
    await screen.findByText(PROXMOX_DENIED);

    goOffline();

    expect(
      await screen.findByText(
        "Notes can only be read while the node is online.",
      ),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Edit node notes" }),
    ).toBeNull();
  });

  it("keeps the notes it had read when a refetch fails for any other reason", async () => {
    serve({ description: "sentinel notes\n", digest: "d1" });
    const { qc } = renderCard();
    expect(await screen.findByText("sentinel notes")).toBeInTheDocument();

    mockedGet.mockRejectedValueOnce(
      new ApiClientError(502, {
        error: "bad_gateway",
        message: "Failed to connect to Proxmox",
      }),
    );
    await act(async () => {
      await qc.invalidateQueries({ queryKey: NOTES_KEY });
    });

    // The failure is noted beside what it did not take away, as before.
    expect(
      await screen.findByText(
        /Could not load this node's notes: Failed to connect to Proxmox/,
      ),
    ).toBeInTheDocument();
    expect(screen.getByText("sentinel notes")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Edit node notes" }),
    ).toBeInTheDocument();
    expect(screen.queryByText(VIEWERS)).toBeNull();
  });

  it("shows the notes again once a read is allowed", async () => {
    mockedGet.mockRejectedValueOnce(refusedByNexara());
    mockedGet.mockResolvedValueOnce({
      description: "sentinel notes\n",
      digest: "d1",
    });
    const { qc } = renderCard();
    expect(await screen.findByText(VIEWERS)).toBeInTheDocument();

    // The permission reaches the server's side, and the notes are read again.
    await act(async () => {
      await qc.invalidateQueries({ queryKey: NOTES_KEY });
    });

    expect(await screen.findByText("sentinel notes")).toBeInTheDocument();
    expect(screen.queryByText(VIEWERS)).toBeNull();
    expect(
      await screen.findByRole("button", { name: "Edit node notes" }),
    ).toBeInTheDocument();
  });
});

describe("editing", () => {
  it("starts with the notes as they were written, less the newline Proxmox adds", async () => {
    const user = userEvent.setup();
    serve({ description: "line one\nline two\n", digest: "d1" });
    renderCard();

    const dialog = await openDialog(user);

    expect(notesField(dialog)).toHaveValue("line one\nline two");
    // Nothing is changed by opening it, so there is nothing to save.
    expect(saveButton(dialog)).toBeDisabled();
  });

  it("sends the notes and the digest it was read at, and nothing else", async () => {
    const user = userEvent.setup();
    serve({ description: "sentinel notes\n", digest: "d1" });
    renderCard();

    const dialog = await openDialog(user);
    await user.click(notesField(dialog));
    await user.keyboard("{Control>}{End}{/Control} and more");
    await user.click(saveButton(dialog));

    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledTimes(1);
    });
    expect(mockedPut).toHaveBeenCalledWith(OPTIONS_URL, {
      description: "sentinel notes and more",
      digest: "d1",
    });
    await waitFor(() => {
      expect(screen.queryByRole("dialog")).toBeNull();
    });
  });

  it("keeps the line breaks it is given", async () => {
    const user = userEvent.setup();
    serve({ digest: "d1" });
    renderCard();

    const dialog = await openDialog(user);
    fireEvent.change(notesField(dialog), {
      target: { value: "line one\n\nline three\n" },
    });
    await user.click(saveButton(dialog));

    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledTimes(1);
    });
    expect(mockedPut).toHaveBeenCalledWith(OPTIONS_URL, {
      description: "line one\n\nline three\n",
      digest: "d1",
    });
  });

  it("removes the notes when they are emptied, through delete", async () => {
    const user = userEvent.setup();
    serve({ description: "sentinel notes\n", digest: "d1" });
    renderCard();

    const dialog = await openDialog(user);
    await user.clear(notesField(dialog));
    await user.click(saveButton(dialog));

    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledTimes(1);
    });
    expect(mockedPut.mock.calls[0]?.[1]).toEqual({
      delete: ["description"],
      digest: "d1",
    });
  });

  it("counts notes of only whitespace as none", async () => {
    const user = userEvent.setup();
    serve({ description: "sentinel notes\n", digest: "d1" });
    renderCard();

    const dialog = await openDialog(user);
    fireEvent.change(notesField(dialog), { target: { value: "  \n\n  " } });
    await user.click(saveButton(dialog));

    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledTimes(1);
    });
    expect(mockedPut.mock.calls[0]?.[1]).toEqual({
      delete: ["description"],
      digest: "d1",
    });
  });

  it("sends nothing for whitespace where there were no notes", async () => {
    const user = userEvent.setup();
    serve({ digest: "d1" });
    renderCard();

    const dialog = await openDialog(user);
    fireEvent.change(notesField(dialog), { target: { value: "   " } });

    expect(saveButton(dialog)).toBeDisabled();
  });

  it("does not count the newline Proxmox adds as a change", async () => {
    const user = userEvent.setup();
    serve({ description: "sentinel notes\n", digest: "d1" });
    renderCard();

    const dialog = await openDialog(user);
    await user.click(notesField(dialog));
    await user.keyboard("x");
    expect(saveButton(dialog)).toBeEnabled();
    await user.keyboard("{Backspace}");

    expect(notesField(dialog)).toHaveValue("sentinel notes");
    expect(saveButton(dialog)).toBeDisabled();
  });

  it("accepts 65536 characters and blocks 65537, counting each character once", async () => {
    const user = userEvent.setup();
    serve({ digest: "d1" });
    renderCard();

    const dialog = await openDialog(user);
    const field = notesField(dialog);
    fireEvent.change(field, { target: { value: "é".repeat(65536) } });
    expect(saveButton(dialog)).toBeEnabled();
    expect(field).not.toHaveAttribute("aria-invalid");

    fireEvent.change(field, { target: { value: "é".repeat(65537) } });
    expect(saveButton(dialog)).toBeDisabled();
    expect(field).toHaveAttribute("aria-invalid", "true");
    expect(
      within(dialog).getByText(
        "Notes can be at most 65536 characters; on Proxmox VE before 8.4 a long note can be refused sooner.",
      ),
    ).toBeInTheDocument();
    // The browser's own limit is not in play: Proxmox's count is what decides.
    expect(field).not.toHaveAttribute("maxlength");

    // Emoji are two UTF-16 units each and one character.
    fireEvent.change(field, { target: { value: "😀".repeat(40000) } });
    expect(saveButton(dialog)).toBeEnabled();
  });
});

describe("the notes dialog", () => {
  it("says who can read the notes in Nexara and in Proxmox, and that they are not Markdown here", async () => {
    const user = userEvent.setup();
    serve({ description: "sentinel notes\n", digest: "d1" });
    renderCard();

    const dialog = await openDialog(user);
    const help = (notesField(dialog).getAttribute("aria-describedby") ?? "")
      .split(" ")
      .map((id) => document.getElementById(id)?.textContent ?? "")
      .join(" ");

    expect(help).toBe(
      "Proxmox shows these notes as Markdown, links included; Nexara shows them as plain text. In Nexara only users who can manage this node can read them; in Proxmox anyone with Sys.Audit on / can. Keep credentials out.",
    );
    // What it used to say, and is not true any more.
    expect(dialog).not.toHaveTextContent("Everyone who can view this node");
  });

  it("does not read the node again when it opens", async () => {
    const user = userEvent.setup();
    serve({ description: "sentinel notes\n", digest: "d1" });
    const { qc } = renderCard();
    await screen.findByRole("button", { name: "Edit node notes" });
    await flush();
    // Stale, without being read again: see the Options card's test of this.
    await act(async () => {
      await qc.invalidateQueries({ queryKey: NOTES_KEY, refetchType: "none" });
    });

    const before = readUrls().length;
    await openDialog(user);
    await flush();

    expect(readUrls()).toHaveLength(before);
    expect(readUrls()).toEqual([NOTES_URL]);
  });
});

describe("a save the node refuses as stale", () => {
  /** Opens the dialog on notes `first`, types, saves, and lets the re-read return `then`. */
  async function refusedThenRead(
    user: UserEvent,
    first: NodeNotes,
    then: NodeNotes | Error,
  ) {
    serve(first);
    mockedPut.mockRejectedValueOnce(
      new ApiClientError(409, { error: "conflict", message: STALE }),
    );
    renderCard();
    const dialog = await openDialog(user);
    fireEvent.change(notesField(dialog), { target: { value: "new notes" } });
    if (then instanceof Error) mockedGet.mockRejectedValueOnce(then);
    else mockedGet.mockResolvedValueOnce(then);
    await user.click(saveButton(dialog));
    return dialog;
  }

  it("says the notes changed in the dialog's own words, re-reads the node and saves again against the new digest", async () => {
    const user = userEvent.setup();
    const reread = deferred<NodeNotes>();
    serve({ description: "sentinel notes\n", digest: "d1" });
    mockedPut
      .mockRejectedValueOnce(
        new ApiClientError(409, { error: "conflict", message: STALE }),
      )
      .mockResolvedValueOnce({ status: "ok" });
    renderCard();

    const dialog = await openDialog(user);
    fireEvent.change(notesField(dialog), {
      target: { value: "new notes" },
    });
    mockedGet.mockImplementationOnce(() => reread.promise);
    await user.click(saveButton(dialog));

    // Not the server's "reload and try again": the notes field does not reload.
    expect(await within(dialog).findByRole("alert")).toHaveTextContent(CHANGED);
    expect(dialog).not.toHaveTextContent("reload and try again");
    expect(
      within(dialog).getByText("Reading this node's current configuration…"),
    ).toBeInTheDocument();
    expect(saveButton(dialog)).toBeDisabled();

    reread.resolve({ description: "someone else's notes\n", digest: "d2" });
    // What is stored now is shown where the operator is looking, read-only: the
    // card behind the overlay shows it too, but cannot be seen.
    expect(
      await within(dialog).findByText(
        "Notes now stored on the node — saving replaces them:",
      ),
    ).toBeInTheDocument();
    expect(
      within(dialog).getByText("someone else's notes"),
    ).toBeInTheDocument();
    expect(screen.getAllByText("someone else's notes")).toHaveLength(2);
    // What was typed stays.
    expect(notesField(dialog)).toHaveValue("new notes");
    expect(saveButton(dialog)).toBeEnabled();

    await user.click(saveButton(dialog));
    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledTimes(2);
    });
    expect(mockedPut.mock.calls[1]?.[1]).toEqual({
      description: "new notes",
      digest: "d2",
    });
  });

  it("shows the notes now stored as plain text that cannot be edited", async () => {
    const user = userEvent.setup();
    const hostile = '<b>x</b> <img src=x onerror="alert(1)"> **bold**';
    const dialog = await refusedThenRead(
      user,
      { description: "sentinel notes\n", digest: "d1" },
      { description: `${hostile}\nsecond line\n`, digest: "d2" },
    );

    const block = await within(dialog).findByText(
      (_, el) => el?.textContent === `${hostile}\nsecond line`,
    );
    expect(block.children).toHaveLength(0);
    expect(block).toHaveClass("whitespace-pre-wrap", "font-mono");
    // Scrollable from the keyboard, and not a field.
    expect(block).toHaveAttribute("tabindex", "0");
    expect(within(dialog).getAllByRole("textbox")).toHaveLength(1);
    expect(dialog.querySelector("b, img, a")).toBeNull();
  });

  it("says nothing of the notes when they are what the dialog opened with", async () => {
    const user = userEvent.setup();
    // The digest covers the whole file: its ACME keys, or the options.
    const dialog = await refusedThenRead(
      user,
      { description: "sentinel notes\n", digest: "d1" },
      { description: "sentinel notes\n", digest: "d2" },
    );

    expect(
      await within(dialog).findByText(
        "The node's notes are unchanged; something else in its configuration changed. Saving again writes the notes shown here.",
      ),
    ).toBeInTheDocument();
    expect(dialog).not.toHaveTextContent(/Notes now stored on the node/);
    // Where the card behind shows the same notes once, the dialog shows none.
    expect(within(dialog).queryByText("sentinel notes")).toBeNull();
  });

  it("does not count the newline Proxmox adds as the notes having changed", async () => {
    const user = userEvent.setup();
    const dialog = await refusedThenRead(
      user,
      { description: "sentinel notes\n", digest: "d1" },
      { description: "sentinel notes", digest: "d2" },
    );

    expect(
      await within(dialog).findByText(/The node's notes are unchanged/),
    ).toBeInTheDocument();
  });

  it("says when the notes were removed meanwhile", async () => {
    const user = userEvent.setup();
    const dialog = await refusedThenRead(
      user,
      { description: "sentinel notes\n", digest: "d1" },
      { digest: "d2" },
    );

    expect(
      await within(dialog).findByText(
        "The notes were removed from the node in the meantime. Saving again writes the notes shown here.",
      ),
    ).toBeInTheDocument();
    expect(dialog).not.toHaveTextContent(/Notes now stored on the node/);
  });

  it("shows no notes of a read that failed, and keeps the pin", async () => {
    const user = userEvent.setup();
    const dialog = await refusedThenRead(
      user,
      { description: "sentinel notes\n", digest: "d1" },
      new ApiClientError(502, {
        error: "bad_gateway",
        message: "Failed to connect to Proxmox",
      }),
    );

    expect(
      await within(dialog).findByText(
        "Nexara could not re-read this node's configuration, so saving again will be refused again until it can.",
      ),
    ).toBeInTheDocument();
    expect(dialog).not.toHaveTextContent(/Notes now stored on the node/);
    expect(saveButton(dialog)).toBeEnabled();
  });

  it("keeps the notes now stored out of the live region, which gets the one sentence", async () => {
    const user = userEvent.setup();
    const dialog = await refusedThenRead(
      user,
      { description: "sentinel notes\n", digest: "d1" },
      { description: "someone else's notes\n", digest: "d2" },
    );

    const sentence = await within(dialog).findByText(
      "Notes now stored on the node — saving replaces them:",
    );
    const block = within(dialog).getByText("someone else's notes");
    // role="status" is polite and read out whole when it changes: the sentence
    // belongs in it, and notes of up to 64 KiB do not.
    const status = within(dialog).getByRole("status");
    expect(status).toContainElement(sentence);
    expect(status).not.toContainElement(block);
    expect(status).not.toHaveTextContent("someone else's notes");
    expect(block.closest('[role="status"]')).toBeNull();
    // Still where the operator can read them: in the dialog, and focusable.
    expect(dialog).toContainElement(block);
    expect(block).toHaveAttribute("tabindex", "0");
  });

  it("shows the notes now stored only while the re-read that found them is the latest word", async () => {
    const user = userEvent.setup();
    serve({ description: "sentinel notes\n", digest: "d1" });
    mockedPut
      .mockRejectedValueOnce(
        new ApiClientError(409, { error: "conflict", message: STALE }),
      )
      .mockRejectedValueOnce(
        new ApiClientError(409, { error: "conflict", message: STALE }),
      );
    renderCard();
    const dialog = await openDialog(user);
    fireEvent.change(notesField(dialog), { target: { value: "new notes" } });
    mockedGet.mockResolvedValueOnce({
      description: "someone else's notes\n",
      digest: "d2",
    });
    await user.click(saveButton(dialog));
    expect(
      await within(dialog).findByText(
        "Notes now stored on the node — saving replaces them:",
      ),
    ).toBeInTheDocument();
    expect(
      within(dialog).getByText("someone else's notes"),
    ).toBeInTheDocument();

    // Saved again and refused again, and this time the node cannot be read:
    // the notes the first read found are not what to show for this conflict.
    mockedGet.mockRejectedValueOnce(
      new ApiClientError(502, {
        error: "bad_gateway",
        message: "Failed to connect to Proxmox",
      }),
    );
    await user.click(saveButton(dialog));

    expect(
      await within(dialog).findByText(
        "Nexara could not re-read this node's configuration, so saving again will be refused again until it can.",
      ),
    ).toBeInTheDocument();
    expect(dialog).not.toHaveTextContent(/Notes now stored on the node/);
    expect(within(dialog).queryByText("someone else's notes")).toBeNull();
  });
});

describe("focus", () => {
  it("goes back to the Edit button when the dialog closes", async () => {
    const user = userEvent.setup();
    serve({ description: "sentinel notes\n", digest: "d1" });
    renderCard();

    const edit = await screen.findByRole("button", { name: "Edit node notes" });
    const dialog = await openDialog(user);
    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));

    await waitFor(() => {
      expect(edit).toHaveFocus();
    });
  });

  it("falls back to the card when the Edit button is gone by then", async () => {
    const user = userEvent.setup();
    serve({ description: "sentinel notes\n", digest: "d1" });
    const { goOffline } = renderCard();

    const dialog = await openDialog(user);
    goOffline();
    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    await waitFor(() => {
      expect(screen.queryByRole("dialog")).toBeNull();
    });

    const card = screen.getByText("Notes").closest('[tabindex="-1"]');
    expect(card).not.toBeNull();
    await waitFor(() => {
      expect(card).toHaveFocus();
    });
  });
});

describe("a save that fails after its dialog was dismissed", () => {
  it("toasts it once, naming the notes and the node", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    serve({ description: "sentinel notes\n", digest: "d1" });
    mockedPut.mockReset();
    mockedPut.mockReturnValueOnce(held.promise);
    renderCard();

    const dialog = await openDialog(user);
    fireEvent.change(notesField(dialog), { target: { value: "new notes" } });
    await user.click(saveButton(dialog));
    await within(dialog).findByRole("button", { name: "Saving..." });
    await user.keyboard("{Escape}");
    await waitFor(() => {
      expect(screen.queryByRole("dialog")).toBeNull();
    });

    held.reject(
      new ApiClientError(403, {
        error: "forbidden",
        message: "Proxmox API permission denied",
      }),
    );
    await waitFor(() => {
      expect(mockedToastError).toHaveBeenCalledWith(
        `Saving the notes of ${NODE} failed: Proxmox API permission denied`,
      );
    });
    await flush();
    expect(mockedToastError).toHaveBeenCalledTimes(1);
  });
});
