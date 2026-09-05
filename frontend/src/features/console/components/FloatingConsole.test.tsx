import { describe, it, expect, vi, beforeEach } from "vitest";
import { act } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { FloatingConsole } from "./FloatingConsole";
import { useConsoleStore } from "@/stores/console-store";
import type { ConsoleTab } from "../types/console";
import { vncTab } from "../console.fixtures";

// These tests are about the shape of the tree FloatingConsole returns, not
// about what a viewer does with a socket, so both viewers are stubbed down to
// a marker element. React preserves a host instance only while its fiber
// survives, so DOM node identity is a direct read of "did this remount?" —
// the same thing you can watch happen to the noVNC <canvas> in a browser.
vi.mock("./VNCViewer", () => ({
  VNCViewer: ({ tab, visible }: { tab: ConsoleTab; visible: boolean }) => (
    <div
      data-testid="content-vnc"
      data-tab-id={tab.id}
      data-visible={String(visible)}
    />
  ),
}));

vi.mock("./Terminal", () => ({
  Terminal: ({ tab, visible }: { tab: ConsoleTab; visible: boolean }) => (
    <div
      data-testid="content-term"
      data-tab-id={tab.id}
      data-visible={String(visible)}
    />
  ),
}));

// Chrome that pulls its own server state; irrelevant to tree shape.
vi.mock("./ConsoleTabBar", () => ({ ConsoleTabBar: () => null }));
vi.mock("./QuickConnect", () => ({ QuickConnect: () => null }));

const tab = vncTab({ status: "connected" });

function content(): Element | null {
  return document.querySelector("[data-tab-id]");
}

beforeEach(() => {
  useConsoleStore.setState({
    tabs: [tab],
    activeTabId: tab.id,
    windowMode: "floating",
  });
});

// NOTE: only the first test below pins the reported bug. The other three are
// merge-regression guards for the PiP/window chrome that the unification
// folded together, and they pass against the pre-fix code too. If you prune
// this file, keep the first one.
describe("FloatingConsole window modes", () => {
  it("keeps the same console content element across every window mode", () => {
    // Minimizing used to return a separate picture-in-picture tree. React
    // reconciles children by position, so that swapped a <TitleBar> in where
    // a <div> had been and unmounted the whole subtree — killing the live
    // RFB/WebSocket and re-dialling (re-minting, re-auditing) a Proxmox
    // console on every minimize and every restore, while the outgoing
    // connection's late disconnect event stamped "disconnected" over its
    // replacement. Floating and maximized already shared a tree, which is
    // exactly why only minimize showed the bug.
    renderWithProviders(<FloatingConsole />);
    const original = content();
    expect(original).not.toBeNull();

    const setWindowMode = useConsoleStore.getState().setWindowMode;
    for (const mode of [
      "maximized",
      "minimized",
      "floating",
      "minimized",
      "maximized",
    ] as const) {
      act(() => {
        setWindowMode(mode);
      });
      // Same DOM node throughout: the viewer was never torn down, so neither
      // was its socket.
      expect(content()).toBe(original);
    }
  });

  it("still renders the minimized picture-in-picture chrome", () => {
    // The unified tree must not have cost the PiP its controls or label.
    const { getByTitle, getByText, getByTestId } = renderWithProviders(
      <FloatingConsole />,
    );
    act(() => {
      useConsoleStore.getState().setWindowMode("minimized");
    });

    expect(getByTitle("Restore")).toBeInTheDocument();
    expect(getByTitle("Close")).toBeInTheDocument();
    expect(getByText("VNC: linux11")).toBeInTheDocument();
    expect(getByTestId("content-vnc")).toBeInTheDocument();
    // The title bar's own controls belong to the restored window only.
    expect(document.querySelector('[title="Minimize"]')).toBeNull();
  });

  it("renders the full window chrome when restored", () => {
    const { getByTitle } = renderWithProviders(<FloatingConsole />);
    act(() => {
      useConsoleStore.getState().setWindowMode("minimized");
    });
    act(() => {
      useConsoleStore.getState().showConsole();
    });

    expect(getByTitle("Minimize")).toBeInTheDocument();
    expect(getByTitle("Maximize")).toBeInTheDocument();
    expect(document.querySelector('[title="Restore"]')).toBeNull();
  });

  it("tears the content down when the console is hidden", () => {
    renderWithProviders(<FloatingConsole />);
    expect(content()).not.toBeNull();

    act(() => {
      useConsoleStore.getState().setWindowMode("hidden");
    });

    expect(content()).toBeNull();
  });
});
