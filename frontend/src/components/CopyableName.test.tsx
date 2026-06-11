import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, waitFor, fireEvent } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { CopyableName } from "./CopyableName";

function setSecureContext(value: boolean) {
  Object.defineProperty(window, "isSecureContext", {
    value,
    configurable: true,
  });
}

// jsdom has no execCommand; install a shim without referencing the
// deprecated member directly (keeps @typescript-eslint/no-deprecated happy).
function stubExecCommand(impl: () => boolean) {
  const mock = vi.fn(impl);
  Object.defineProperty(document, "execCommand", {
    value: mock,
    configurable: true,
  });
  return mock;
}

describe("CopyableName", () => {
  beforeEach(() => {
    setSecureContext(true);
  });

  afterEach(() => {
    Reflect.deleteProperty(document, "execCommand");
  });

  it("renders the name with a click-to-copy affordance", () => {
    render(<CopyableName name="pve-node-01" />);
    const chip = screen.getByRole("button", { name: "pve-node-01" });
    expect(chip).toHaveAttribute("title", "Click to copy");
  });

  it("copies the name via the clipboard API on click", async () => {
    const user = userEvent.setup();
    render(<CopyableName name="pve-node-01" />);

    const chip = screen.getByRole("button", { name: "pve-node-01" });
    await user.click(chip);

    await waitFor(() => {
      expect(chip).toHaveAttribute("title", "Copied!");
    });
    expect(await window.navigator.clipboard.readText()).toBe("pve-node-01");
  });

  it("copies on Enter for keyboard users", async () => {
    userEvent.setup(); // installs the clipboard stub
    render(<CopyableName name="ceph-pool" />);

    const chip = screen.getByRole("button", { name: "ceph-pool" });
    fireEvent.keyDown(chip, { key: "Enter" });

    await waitFor(() => {
      expect(chip).toHaveAttribute("title", "Copied!");
    });
    expect(await window.navigator.clipboard.readText()).toBe("ceph-pool");
  });

  it("falls back to execCommand in non-secure contexts", async () => {
    setSecureContext(false);
    let copiedValue: string | undefined;
    const exec = stubExecCommand(() => {
      copiedValue = document.querySelector("textarea")?.value;
      return true;
    });

    render(<CopyableName name="my-vm" />);
    const chip = screen.getByRole("button", { name: "my-vm" });
    fireEvent.click(chip);

    await waitFor(() => {
      expect(chip).toHaveAttribute("title", "Copied!");
    });
    expect(exec).toHaveBeenCalledWith("copy");
    expect(copiedValue).toBe("my-vm");
  });

  it("shows a failure hint when no copy mechanism works", async () => {
    setSecureContext(false);
    stubExecCommand(() => false);

    render(<CopyableName name="my-vm" />);
    const chip = screen.getByRole("button", { name: "my-vm" });
    fireEvent.click(chip);

    await waitFor(() => {
      expect(chip).toHaveAttribute("title", expect.stringContaining("Copy failed"));
    });
  });
});
