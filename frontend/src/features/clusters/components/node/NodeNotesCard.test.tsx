import { beforeEach, describe, expect, it, vi } from "vitest";
import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { toast } from "sonner";

import { apiClient, ApiClientError } from "@/lib/api-client";
import { createAppQueryClient } from "@/test/app-query-client";
import { deferred } from "@/test/fake-server";
import { DENIED, denied, flushInAct } from "@/test/save-outcome-kit";
import { renderWithProviders } from "@/test/test-utils";
import { fill } from "@/test/user";
import type { NodeNotes } from "../../api/node-options-queries";
import { NodeNotesCard } from "./NodeNotesCard";

/**
 * The Notes card shows a node's notes as plain text and edits them in a dialog
 * mounted afresh for each open, as the Options card's is: the digest pin, the
 * 409 re-read and the failure toast are tested through that card
 * (NodeOptionsCard.test.tsx), and here once more only for what notes add.
 *
 *   - They are read from a route of their own that answers only users who can
 *     manage the node, so anyone else's card asks for nothing.
 *   - They are free text anyone who can edit the node wrote: never markup.
 *   - After a 409 the notes now stored are shown in the dialog, since saving
 *     replaces them and the card that shows them is behind the overlay.
 */

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

vi.mock("sonner", async () => (await import("@/test/mocks")).sonnerMock());

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
const NOW_STORED = "Notes now stored on the node — saving replaces them:";
const COULD_NOT_REREAD =
  "Nexara could not re-read this node's configuration, so saving again will be refused again until it can.";
const SENTINEL: NodeNotes = { description: "sentinel notes\n", digest: "d1" };

type UserEvent = ReturnType<typeof userEvent.setup>;

function conflict(): ApiClientError {
  return new ApiClientError(409, { error: "conflict", message: STALE });
}

function badGateway(): ApiClientError {
  return new ApiClientError(502, {
    error: "bad_gateway",
    message: "Failed to connect to Proxmox",
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

/** Renders the card on the app's own kind of client; `rerender` changes props. */
function renderCard(over: { online?: boolean; canEdit?: boolean } = {}) {
  const ui = (online: boolean, canEdit: boolean) => (
    <NodeNotesCard
      clusterId={CLUSTER}
      nodeName={NODE}
      online={online}
      canEdit={canEdit}
    />
  );
  const online = over.online ?? true;
  const canEdit = over.canEdit ?? true;
  const view = renderWithProviders(ui(online, canEdit), {
    client: createAppQueryClient(),
    router: false,
  });
  return {
    qc: view.queryClient,
    rerender: (next: { online?: boolean; canEdit?: boolean }) => {
      view.rerender(ui(next.online ?? online, next.canEdit ?? canEdit));
    },
  };
}

const editButton = () =>
  screen.findByRole("button", { name: "Edit node notes" });

function expectNoEdit() {
  expect(screen.queryByRole("button", { name: "Edit node notes" })).toBeNull();
}

async function openDialog(user: UserEvent): Promise<HTMLElement> {
  await user.click(await editButton());
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

  // Which descriptions count as none is visibleNotes' (lib/node-options.test.ts).
  it.each([
    ["unset", { digest: "d1" }],
    ["only whitespace", { description: "  \n \n", digest: "d1" }],
  ])("says there are none when they are %s", async (_, options) => {
    serve(options);
    renderCard();

    expect(await screen.findByText("No notes.")).toBeInTheDocument();
  });

  it("does not read an offline node, and says why", async () => {
    serve(SENTINEL);
    renderCard({ online: false });

    expect(
      await screen.findByText(
        "Notes can only be read while the node is online.",
      ),
    ).toBeInTheDocument();
    await flushInAct();
    expect(mockedGet).not.toHaveBeenCalled();
    expectNoEdit();
  });

  it("reads the notes route, and no other, under the node's own prefix", async () => {
    serve(SENTINEL);
    const { qc } = renderCard();

    expect(await screen.findByText("sentinel notes")).toBeInTheDocument();
    expect(readUrls()).toEqual([NOTES_URL]);
    expect(qc.getQueryData(NOTES_KEY)).toEqual(SENTINEL);

    // useSetNodeACMEConfig invalidates this prefix after a save: the file's
    // digest moved, and the notes read holds it.
    await act(async () => {
      await qc.invalidateQueries({
        queryKey: ["clusters", CLUSTER, "nodes", NODE],
      });
    });

    expect(readUrls()).toEqual([NOTES_URL, NOTES_URL]);
  });

  it.each([true, false])(
    "asks for nothing from someone who cannot manage nodes, and says who can read the notes (online: %s)",
    async (online) => {
      serve(SENTINEL);
      renderCard({ canEdit: false, online });

      expect(await screen.findByText(VIEWERS)).toBeInTheDocument();
      await flushInAct();
      // The route would answer 403: not a request to make, and not a failure
      // to show.
      expect(mockedGet).not.toHaveBeenCalled();
      for (const text of [
        "sentinel notes",
        "No notes.",
        /Could not load/,
        "Notes can only be read while the node is online.",
      ]) {
        expect(screen.queryByText(text)).toBeNull();
      }
      expectNoEdit();
    },
  );

  it("starts reading the notes once the user is allowed to", async () => {
    serve(SENTINEL);
    const { rerender } = renderCard({ canEdit: false });
    expect(await screen.findByText(VIEWERS)).toBeInTheDocument();
    await flushInAct();
    expect(mockedGet).not.toHaveBeenCalled();

    // A refreshed token carries the permission, and the card is the same one.
    rerender({ canEdit: true });

    expect(await screen.findByText("sentinel notes")).toBeInTheDocument();
    expect(screen.queryByText(VIEWERS)).toBeNull();
    expect(readUrls()).toEqual([NOTES_URL]);
    expect(await editButton()).toBeInTheDocument();
  });

  it("offers a retry for a read that failed, and no Edit", async () => {
    mockedGet.mockRejectedValueOnce(badGateway());
    renderCard();

    expect(
      await screen.findByText("Could not load this node's notes."),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Retry" })).toBeInTheDocument();
    expectNoEdit();
  });
});

// A 403 on the notes read withdraws what the card had read, its notes and its
// Edit. What it then says depends on who refused. Nexara's own check (the
// canManage the card goes by is flat and its cache can lag the server's) is the
// viewer's state, with no Retry. Anything else that answers 403, Proxmox's in
// practice, says nothing about the user: a failure like any other, with its
// message and a Retry. Which is which is notesReadRefusal's, by the message
// alone (api/node-options-queries.test.ts); here is what the card does with it.
describe("a manager the server refuses", () => {
  // requireClusterPerm's word for word (internal/api/handlers/permission.go).
  const nexaraRefusal = () =>
    new ApiClientError(403, {
      error: "forbidden",
      message: "Insufficient permissions",
    });

  /** Reads the notes, then has the next read refused. */
  async function readThenRefused(refusal: ApiClientError) {
    serve(SENTINEL);
    const rendered = renderCard();
    expect(await screen.findByText("sentinel notes")).toBeInTheDocument();
    expect(await editButton()).toBeInTheDocument();

    mockedGet.mockRejectedValueOnce(refusal);
    await act(async () => {
      await rendered.qc.invalidateQueries({ queryKey: NOTES_KEY });
    });
    return rendered;
  }

  it("is shown the viewer's sentence on a first read that Nexara refuses, with no Retry and no Edit", async () => {
    mockedGet.mockRejectedValueOnce(nexaraRefusal());
    renderCard();

    expect(await screen.findByText(VIEWERS)).toBeInTheDocument();
    // A retry would be refused again, and the refusal is not a failure to
    // report as one.
    expect(screen.queryByRole("button", { name: "Retry" })).toBeNull();
    expect(screen.queryByText(/Could not load/)).toBeNull();
    expect(screen.queryByText("Insufficient permissions")).toBeNull();
    expectNoEdit();
    expect(readUrls()).toEqual([NOTES_URL]);
  });

  it("loses the notes it had read when a refetch is refused by Nexara, and Edit with them", async () => {
    const { qc } = await readThenRefused(nexaraRefusal());

    expect(await screen.findByText(VIEWERS)).toBeInTheDocument();
    // Still in the cache, and no longer on screen: what the server now refuses
    // to give is not for this user to keep reading.
    expect(qc.getQueryData(NOTES_KEY)).toEqual(SENTINEL);
    expect(screen.queryByText("sentinel notes")).toBeNull();
    expect(screen.queryByText("No notes.")).toBeNull();
    expectNoEdit();
    expect(screen.queryByRole("button", { name: "Retry" })).toBeNull();
    expect(screen.queryByText(/Could not load/)).toBeNull();
  });

  it("shows a first read that Proxmox refuses as the failure it is, with its message and a Retry, and no Edit", async () => {
    mockedGet.mockRejectedValueOnce(denied());
    renderCard();

    expect(
      await screen.findByText("Could not load this node's notes."),
    ).toBeInTheDocument();
    expect(screen.getByText(DENIED)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Retry" })).toBeInTheDocument();
    expect(screen.queryByText(VIEWERS)).toBeNull();
    expectNoEdit();
  });

  it("withdraws the notes it had read when a refetch is refused by Proxmox, shows why with a Retry, and has them back on it", async () => {
    const user = userEvent.setup();
    const { qc } = await readThenRefused(denied());

    // The failure, and not the viewer's state: nothing here is about the user.
    expect(
      await screen.findByText("Could not load this node's notes."),
    ).toBeInTheDocument();
    expect(screen.getByText(DENIED)).toBeInTheDocument();
    expect(screen.queryByText(VIEWERS)).toBeNull();
    // But what was read is withdrawn all the same, and Edit with it.
    expect(screen.queryByText("sentinel notes")).toBeNull();
    expect(screen.queryByText("No notes.")).toBeNull();
    expectNoEdit();
    expect(qc.getQueryData(NOTES_KEY)).toEqual(SENTINEL);

    await user.click(screen.getByRole("button", { name: "Retry" }));

    expect(await screen.findByText("sentinel notes")).toBeInTheDocument();
    expect(screen.queryByText(DENIED)).toBeNull();
    expect(await editButton()).toBeInTheDocument();
  });

  it("says the node is offline, not the failure, for a refusal that is stale by then", async () => {
    mockedGet.mockRejectedValueOnce(denied());
    const { rerender } = renderCard();
    await screen.findByText(DENIED);

    rerender({ online: false });

    expect(
      await screen.findByText(
        "Notes can only be read while the node is online.",
      ),
    ).toBeInTheDocument();
    expectNoEdit();
  });

  it("keeps the notes it had read when a refetch fails for any other reason", async () => {
    serve(SENTINEL);
    const { qc } = renderCard();
    expect(await screen.findByText("sentinel notes")).toBeInTheDocument();

    mockedGet.mockRejectedValueOnce(badGateway());
    await act(async () => {
      await qc.invalidateQueries({ queryKey: NOTES_KEY });
    });

    // The failure is noted beside what it did not take away.
    expect(
      await screen.findByText(
        /Could not load this node's notes: Failed to connect to Proxmox/,
      ),
    ).toBeInTheDocument();
    expect(screen.getByText("sentinel notes")).toBeInTheDocument();
    expect(await editButton()).toBeInTheDocument();
    expect(screen.queryByText(VIEWERS)).toBeNull();
  });

  it("shows the notes again once a read is allowed", async () => {
    mockedGet.mockRejectedValueOnce(nexaraRefusal());
    mockedGet.mockResolvedValueOnce(SENTINEL);
    const { qc } = renderCard();
    expect(await screen.findByText(VIEWERS)).toBeInTheDocument();

    // The permission reaches the server's side, and the notes are read again.
    await act(async () => {
      await qc.invalidateQueries({ queryKey: NOTES_KEY });
    });

    expect(await screen.findByText("sentinel notes")).toBeInTheDocument();
    expect(screen.queryByText(VIEWERS)).toBeNull();
    expect(await editButton()).toBeInTheDocument();
  });
});

describe("editing", () => {
  it("starts with the notes as they were written, less the newline Proxmox adds, and says who can read them", async () => {
    const user = userEvent.setup();
    serve({ description: "line one\nline two\n", digest: "d1" });
    renderCard();

    const dialog = await openDialog(user);

    expect(notesField(dialog)).toHaveValue("line one\nline two");
    // Nothing is changed by opening it, so there is nothing to save.
    expect(saveButton(dialog)).toBeDisabled();
    const help = (notesField(dialog).getAttribute("aria-describedby") ?? "")
      .split(" ")
      .map((id) => document.getElementById(id)?.textContent ?? "")
      .join(" ");
    expect(help).toBe(
      "Proxmox shows these notes as Markdown, links included; Nexara shows them as plain text. In Nexara only users who can manage this node can read them; in Proxmox anyone with Sys.Audit on / can. Keep credentials out.",
    );
    expect(dialog).not.toHaveTextContent("Everyone who can view this node");
  });

  it("sends the notes as typed, line breaks included, with the digest it was read at and nothing else", async () => {
    const user = userEvent.setup();
    serve(SENTINEL);
    renderCard();

    const dialog = await openDialog(user);
    fill(notesField(dialog), "sentinel notes\n\nand more\n");
    await user.click(saveButton(dialog));

    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledTimes(1);
    });
    expect(mockedPut).toHaveBeenCalledWith(OPTIONS_URL, {
      description: "sentinel notes\n\nand more\n",
      digest: "d1",
    });
    await waitFor(() => {
      expect(screen.queryByRole("dialog")).toBeNull();
    });
  });

  it("removes the notes when they are emptied, through delete", async () => {
    const user = userEvent.setup();
    serve(SENTINEL);
    renderCard();

    const dialog = await openDialog(user);
    fill(notesField(dialog), "");
    await user.click(saveButton(dialog));

    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledTimes(1);
    });
    expect(mockedPut.mock.calls[0]?.[1]).toEqual({
      delete: ["description"],
      digest: "d1",
    });
  });

  // How checkNotes counts (code points, not UTF-16 units) is its own test.
  it("holds Save past 65536 characters, with the browser's own limit out of play", async () => {
    const user = userEvent.setup();
    serve({ digest: "d1" });
    renderCard();

    const dialog = await openDialog(user);
    fill(notesField(dialog), "😀".repeat(65536));
    expect(saveButton(dialog)).toBeEnabled();
    expect(notesField(dialog)).not.toHaveAttribute("aria-invalid");

    fill(notesField(dialog), "é".repeat(65537));
    expect(saveButton(dialog)).toBeDisabled();
    expect(notesField(dialog)).toHaveAttribute("aria-invalid", "true");
    expect(
      within(dialog).getByText(
        "Notes can be at most 65536 characters; on Proxmox VE before 8.4 a long note can be refused sooner.",
      ),
    ).toBeInTheDocument();
    expect(notesField(dialog)).not.toHaveAttribute("maxlength");
  });

  it("does not read the node again when it opens", async () => {
    const user = userEvent.setup();
    serve(SENTINEL);
    const { qc } = renderCard();
    await editButton();
    await flushInAct();
    // Stale, without being read again: see the Options card's test of this.
    await act(async () => {
      await qc.invalidateQueries({ queryKey: NOTES_KEY, refetchType: "none" });
    });

    await openDialog(user);
    await flushInAct();

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
    mockedPut.mockRejectedValueOnce(conflict());
    renderCard();
    const dialog = await openDialog(user);
    fill(notesField(dialog), "new notes");
    if (then instanceof Error) mockedGet.mockRejectedValueOnce(then);
    else mockedGet.mockResolvedValueOnce(then);
    await user.click(saveButton(dialog));
    return dialog;
  }

  it("says the notes changed in the dialog's own words, re-reads the node and saves again against the new digest", async () => {
    const user = userEvent.setup();
    const reread = deferred<NodeNotes>();
    serve(SENTINEL);
    mockedPut
      .mockRejectedValueOnce(conflict())
      .mockResolvedValueOnce({ status: "ok" });
    renderCard();

    const dialog = await openDialog(user);
    fill(notesField(dialog), "new notes");
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
    expect(await within(dialog).findByText(NOW_STORED)).toBeInTheDocument();
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

  it("shows the notes now stored as plain text that cannot be edited, outside the live region", async () => {
    const user = userEvent.setup();
    const hostile = '<b>x</b> <img src=x onerror="alert(1)"> **bold**';
    const dialog = await refusedThenRead(user, SENTINEL, {
      description: `${hostile}\nsecond line\n`,
      digest: "d2",
    });

    const sentence = await within(dialog).findByText(NOW_STORED);
    const block = within(dialog).getByText(
      (_, el) => el?.textContent === `${hostile}\nsecond line`,
    );
    expect(block.children).toHaveLength(0);
    expect(block).toHaveClass("whitespace-pre-wrap", "font-mono");
    // Scrollable from the keyboard, and not a field.
    expect(block).toHaveAttribute("tabindex", "0");
    expect(within(dialog).getAllByRole("textbox")).toHaveLength(1);
    expect(dialog.querySelector("b, img, a")).toBeNull();
    // role="status" is polite and read out whole when it changes: the sentence
    // belongs in it, and notes of up to 64 KiB do not.
    const status = within(dialog).getByRole("status");
    expect(status).toContainElement(sentence);
    expect(status).not.toContainElement(block);
    expect(status).not.toHaveTextContent("second line");
    expect(dialog).toContainElement(block);
  });

  // Which reads count as the notes having changed is notesChangedBetween's.
  it.each([
    [
      "are what the dialog opened with, and something else changed",
      SENTINEL,
      { description: "sentinel notes", digest: "d2" },
      "The node's notes are unchanged; something else in its configuration changed. Saving again writes the notes shown here.",
    ],
    [
      "were removed meanwhile",
      SENTINEL,
      { digest: "d2" },
      "The notes were removed from the node in the meantime. Saving again writes the notes shown here.",
    ],
  ])("says nothing of the notes when they %s", async (_, first, then, note) => {
    const user = userEvent.setup();
    const dialog = await refusedThenRead(user, first, then);

    expect(await within(dialog).findByText(note)).toBeInTheDocument();
    expect(dialog).not.toHaveTextContent(/Notes now stored on the node/);
    // Where the card behind shows the same notes once, the dialog shows none.
    expect(within(dialog).queryByText("sentinel notes")).toBeNull();
  });

  it("shows the notes now stored only while the re-read that found them is the latest word", async () => {
    const user = userEvent.setup();
    serve(SENTINEL);
    mockedPut
      .mockRejectedValueOnce(conflict())
      .mockRejectedValueOnce(conflict());
    renderCard();
    const dialog = await openDialog(user);
    fill(notesField(dialog), "new notes");
    mockedGet.mockResolvedValueOnce({
      description: "someone else's notes\n",
      digest: "d2",
    });
    await user.click(saveButton(dialog));
    expect(await within(dialog).findByText(NOW_STORED)).toBeInTheDocument();
    expect(
      within(dialog).getByText("someone else's notes"),
    ).toBeInTheDocument();

    // Saved again and refused again, and this time the node cannot be read:
    // the notes the first read found are not what to show for this conflict.
    mockedGet.mockRejectedValueOnce(badGateway());
    await user.click(saveButton(dialog));

    expect(
      await within(dialog).findByText(COULD_NOT_REREAD),
    ).toBeInTheDocument();
    expect(dialog).not.toHaveTextContent(/Notes now stored on the node/);
    expect(within(dialog).queryByText("someone else's notes")).toBeNull();
    expect(saveButton(dialog)).toBeEnabled();
  });
});

describe("focus", () => {
  it("goes back to the Edit button when the dialog closes", async () => {
    const user = userEvent.setup();
    serve(SENTINEL);
    renderCard();

    const edit = await editButton();
    const dialog = await openDialog(user);
    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));

    await waitFor(() => {
      expect(edit).toHaveFocus();
    });
  });

  it("falls back to the card when the Edit button is gone by then", async () => {
    const user = userEvent.setup();
    serve(SENTINEL);
    const { rerender } = renderCard();

    const dialog = await openDialog(user);
    rerender({ online: false });
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
    serve(SENTINEL);
    mockedPut.mockReset();
    mockedPut.mockReturnValueOnce(held.promise);
    renderCard();

    const dialog = await openDialog(user);
    fill(notesField(dialog), "new notes");
    await user.click(saveButton(dialog));
    await within(dialog).findByRole("button", { name: "Saving..." });
    await user.keyboard("{Escape}");
    await waitFor(() => {
      expect(screen.queryByRole("dialog")).toBeNull();
    });

    held.reject(denied());
    await waitFor(() => {
      expect(mockedToastError).toHaveBeenCalledWith(
        `Saving the notes of ${NODE} failed: ${DENIED}`,
      );
    });
    await flushInAct();
    expect(mockedToastError).toHaveBeenCalledTimes(1);
  });
});
