import { afterEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import { PBSKeySaveDialog } from "./PBSKeySaveDialog";

// A key file in the shape proxmox-backup-client writes. Synthetic: the data
// member is a marker, and the fingerprint is placeholder hex.
const KEY =
  '{"kdf":null,"created":"2026-01-01T00:00:00+00:00","data":"CANARY-spa-key-material",' +
  '"fingerprint":"aa:bb:cc:dd:ee:ff:00:11:22:33:44:55:66:77:88:99:aa:bb:cc:dd:ee:ff:00:11:22:33:44:55:66:77:88:99"}';

const CLUSTER = { id: "c1", name: "cluster01" };

function renderDialog(
  keyText = KEY,
  cluster: { id: string; name: string | null } = CLUSTER,
) {
  const onDone = vi.fn();
  // pointerEventsCheck off: a modal Radix layer sets pointer-events: none on
  // <body>, and a click outside the dialog is exactly what one test sends.
  const user = userEvent.setup({ pointerEventsCheck: 0 });
  render(
    <PBSKeySaveDialog
      storage="store01"
      cluster={cluster}
      keyText={keyText}
      onDone={onDone}
    />,
  );
  return { user, onDone };
}

describe("PBSKeySaveDialog", () => {
  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it("shows the key, says Nexara keeps no copy, and holds Done until the box is ticked", async () => {
    const { user, onDone } = renderDialog();

    expect(screen.getByRole("alertdialog")).toBeInTheDocument();
    expect(screen.getByLabelText("Encryption key")).toHaveValue(KEY);
    expect(
      screen.getByText(/Nexara does not keep a copy of this key/),
    ).toBeInTheDocument();

    const done = screen.getByRole("button", { name: "Done" });
    expect(done).toBeDisabled();

    await user.click(screen.getByLabelText("I have saved this key"));
    expect(done).toBeEnabled();
    await user.click(done);
    expect(onDone).toHaveBeenCalledTimes(1);
  });

  // The twin of the next test's outside click: the same click, on the same
  // page, does close a dialog that can be dismissed that way. Without it,
  // "the key dialog stayed open" could mean the click never reached anything.
  it("precondition: a click outside a dismissible dialog closes it", async () => {
    const onOpenChange = vi.fn();
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    render(
      <Dialog open onOpenChange={onOpenChange}>
        <DialogContent>
          <DialogTitle>dismissible</DialogTitle>
        </DialogContent>
      </Dialog>,
    );

    await user.click(document.body);

    expect(onOpenChange).toHaveBeenCalledWith(false);
  });

  it("cannot be dismissed by Escape, a click outside or a close button before the box is ticked", async () => {
    const { user, onDone } = renderDialog();

    await user.keyboard("{Escape}");
    await user.click(document.body);

    expect(onDone).not.toHaveBeenCalled();
    expect(screen.getByRole("alertdialog")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /close/i })).toBeNull();

    // Once ticked, Escape is an ordinary way out again.
    await user.click(screen.getByLabelText("I have saved this key"));
    await user.keyboard("{Escape}");
    expect(onDone).toHaveBeenCalledTimes(1);
  });

  it("takes focus on Copy when it opens, and on the checkbox when the key did not arrive", async () => {
    const { unmount } = render(
      <PBSKeySaveDialog
        storage="store01"
        cluster={CLUSTER}
        keyText={KEY}
        onDone={vi.fn()}
      />,
    );
    await waitFor(() => {
      expect(screen.getByRole("button", { name: "Copy" })).toHaveFocus();
    });
    unmount();

    render(
      <PBSKeySaveDialog
        storage="store01"
        cluster={CLUSTER}
        keyText=""
        onDone={vi.fn()}
      />,
    );
    await waitFor(() => {
      expect(screen.getByLabelText("I have saved this key")).toHaveFocus();
    });
  });

  it("copies from its own key field when the clipboard API is out of reach", async () => {
    // Plain HTTP: no secure context, so copyText takes its legacy path.
    const secure = Object.getOwnPropertyDescriptor(window, "isSecureContext");
    Object.defineProperty(window, "isSecureContext", {
      value: false,
      configurable: true,
    });
    const selected: Element[] = [];
    vi.spyOn(HTMLTextAreaElement.prototype, "select").mockImplementation(
      function (this: HTMLTextAreaElement) {
        selected.push(this);
      },
    );
    let copiedFrom: Element | undefined;
    let focusedAtCopy: Element | null = null;
    const execCommand = vi.fn(() => {
      copiedFrom = selected.at(-1);
      focusedAtCopy = document.activeElement;
      return true;
    });
    Object.defineProperty(document, "execCommand", {
      value: execCommand,
      configurable: true,
    });
    try {
      const { user } = renderDialog();
      // Only what the copy itself selects counts.
      selected.length = 0;

      await user.click(screen.getByRole("button", { name: "Copy" }));

      expect(execCommand).toHaveBeenCalledWith("copy");
      // The dialog's own field, not a temporary one outside its focus trap —
      // selected, and holding focus, when the copy runs.
      const keyField = screen.getByLabelText("Encryption key");
      expect(copiedFrom).toBe(keyField);
      expect(focusedAtCopy).toBe(keyField);
    } finally {
      if (secure) Object.defineProperty(window, "isSecureContext", secure);
      else Reflect.deleteProperty(window, "isSecureContext");
      Reflect.deleteProperty(document, "execCommand");
    }
  });

  it("downloads exactly the key as a .json file", async () => {
    // jsdom has no createObjectURL; a subclass supplies it while `new URL`
    // keeps working for anything else that runs.
    const blobs: Blob[] = [];
    class RecordingURL extends URL {
      static override createObjectURL(blob: Blob): string {
        blobs.push(blob);
        return "blob:pbs-key";
      }
      static override revokeObjectURL(): void {
        // nothing to release
      }
    }
    vi.stubGlobal("URL", RecordingURL);
    const downloads: string[] = [];
    vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (
      this: HTMLAnchorElement,
    ) {
      downloads.push(this.download);
    });
    const { user } = renderDialog();

    await user.click(screen.getByRole("button", { name: "Download" }));

    expect(downloads).toEqual(["store01-encryption-key.json"]);
    expect(blobs).toHaveLength(1);
    const blob = blobs[0];
    if (!blob) throw new Error("no blob was created");
    expect(await blob.text()).toBe(KEY);
    expect(blob.type).toBe("application/json");
  });

  it("names the storage and the cluster it is on", () => {
    renderDialog(KEY, CLUSTER);

    expect(screen.getByRole("alertdialog")).toHaveTextContent(
      "for store01 on cluster cluster01.",
    );
  });

  it("names a cluster whose name is not to hand by its ID", () => {
    renderDialog(KEY, { id: "c1", name: null });

    expect(screen.getByRole("alertdialog")).toHaveTextContent(
      "for store01 on the cluster with ID c1.",
    );
  });

  it("says where the key is when Proxmox did not send it back", () => {
    renderDialog("");

    expect(screen.queryByLabelText("Encryption key")).toBeNull();
    expect(screen.queryByRole("button", { name: "Download" })).toBeNull();
    expect(
      screen.getByText("/etc/pve/priv/storage/store01.enc"),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Done" })).toBeDisabled();
  });
});
