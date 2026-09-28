import { afterEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type RFB from "@novnc/novnc";
import { renderWithProviders } from "@/test/test-utils";
import { stubApi } from "@/test/fetch-stub";
import type { ConsoleTab } from "../types/console";
import { VNCToolbar } from "./VNCToolbar";

const TAB: ConsoleTab = {
  id: "tab-1",
  clusterID: "c1",
  node: "pve-01",
  vmid: 101,
  type: "vm_vnc",
  label: "VNC: linux01",
  status: "connected",
  reconnectKey: 0,
};

// The console as the toolbar uses it for a paste: keys typed, and focus,
// which noVNC gives its canvas.
function fakeConsole() {
  const canvas = document.createElement("canvas");
  canvas.tabIndex = -1;
  document.body.append(canvas);
  const focus = vi.fn(() => {
    canvas.focus();
  });
  const rfb = { focus, sendKey: vi.fn() } as unknown as RFB;
  return { rfb, focus, canvas };
}

afterEach(() => {
  vi.unstubAllGlobals();
  for (const canvas of document.querySelectorAll("canvas")) canvas.remove();
});

async function openPaste() {
  stubApi({});
  const vnc = fakeConsole();
  const user = userEvent.setup();
  renderWithProviders(<VNCToolbar rfb={vnc.rfb} tab={TAB} />);
  const paste = screen.getByRole("button", { name: "Paste text into console" });
  await user.click(paste);
  const dialog = await screen.findByRole("dialog");
  return { user, paste, dialog, ...vnc };
}

describe("VNCToolbar — paste", () => {
  // To go on typing into the guest. The dialog's focus trap undoes a focus()
  // made while it is still open, so it is made as the dialog closes.
  it("gives focus to the console once the text is sent", async () => {
    const { user, dialog, canvas } = await openPaste();
    await user.type(within(dialog).getByRole("textbox"), "ls");

    await user.click(within(dialog).getByRole("button", { name: "Send" }));

    await waitFor(() => {
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    });
    expect(document.activeElement).toBe(canvas);
  });

  it("gives focus back to the Paste button when cancelled", async () => {
    const { user, paste, dialog, focus } = await openPaste();

    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));

    await waitFor(() => {
      expect(document.activeElement).toBe(paste);
    });
    expect(focus).not.toHaveBeenCalled();
  });
});
