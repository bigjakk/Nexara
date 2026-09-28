import { useRef } from "react";
import { describe, it, expect, beforeEach, afterEach } from "vitest";
import { fireEvent, screen, within, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/test-utils";
import { ResourceTable } from "./ResourceTable";
import type { InventoryRow } from "../types/inventory";
import { useVMContextMenuStore } from "@/stores/vm-context-menu-store";

// Mock localStorage
const localStorageMock = (() => {
  let store = new Map<string, string>();
  return {
    getItem: (key: string) => store.get(key) ?? null,
    setItem: (key: string, value: string) => {
      store.set(key, value);
    },
    removeItem: (key: string) => {
      store.delete(key);
    },
    clear: () => {
      store = new Map();
    },
  };
})();
Object.defineProperty(window, "localStorage", { value: localStorageMock });

function makeRow(
  overrides: Partial<InventoryRow> & { key: string },
): InventoryRow {
  return {
    id: "id-1",
    type: "vm",
    name: "test-vm",
    status: "running",
    clusterName: "Cluster1",
    clusterId: "c1",
    nodeName: "node1",
    vmid: 100,
    cpuCount: 4,
    memTotal: 8589934592,
    diskTotal: 107374182400,
    uptime: 86400,
    tags: "",
    haState: "",
    pool: "",
    template: false,
    ostype: "",
    configOstype: "",
    cpuPercent: 50,
    memPercent: 60,
    diskReadBps: null,
    diskWriteBps: null,
    netInBps: null,
    netOutBps: null,
    ...overrides,
  };
}

function generateRows(count: number): InventoryRow[] {
  return Array.from({ length: count }, (_, i) =>
    makeRow({
      key: `c1:vm:${String(i)}`,
      id: `vm-${String(i)}`,
      name: `vm-${String(i).padStart(4, "0")}`,
      vmid: 100 + i,
      cpuPercent: Math.round((i / count) * 100),
      memPercent: Math.round(((count - i) / count) * 100),
    }),
  );
}

describe("ResourceTable", () => {
  beforeEach(() => {
    localStorageMock.clear();
  });

  it("renders empty state when no data", () => {
    renderWithProviders(<ResourceTable data={[]} />);
    expect(screen.getByText("No resources found.")).toBeInTheDocument();
  });

  it("renders rows with name and status", () => {
    const data = [
      makeRow({ key: "c1:vm:1", name: "web-01", status: "running" }),
      makeRow({ key: "c1:vm:2", name: "db-01", status: "stopped" }),
    ];
    renderWithProviders(<ResourceTable data={data} />);
    expect(screen.getByText("web-01")).toBeInTheDocument();
    expect(screen.getByText("db-01")).toBeInTheDocument();
    expect(screen.getByText("Running")).toBeInTheDocument();
    expect(screen.getByText("Stopped")).toBeInTheDocument();
  });

  it("shows correct resource count", () => {
    const data = generateRows(50);
    renderWithProviders(<ResourceTable data={data} />);
    expect(screen.getByText("50 resources total")).toBeInTheDocument();
  });

  it("paginates with default page size of 25", () => {
    const data = generateRows(50);
    renderWithProviders(<ResourceTable data={data} />);
    expect(screen.getByText("Page 1 of 2")).toBeInTheDocument();
  });

  it("navigates to next page", async () => {
    const user = userEvent.setup();
    const data = generateRows(50);
    renderWithProviders(<ResourceTable data={data} />);

    // Find the forward navigation button by checking all buttons
    const buttons = screen.getAllByRole("button");
    const navButtons = buttons.filter(
      (b) =>
        !b.hasAttribute("disabled") && b.closest(".flex.items-center.gap-2"),
    );
    if (navButtons.length >= 2 && navButtons[1]) {
      await user.click(navButtons[1]);
      expect(screen.getByText("Page 2 of 2")).toBeInTheDocument();
    }
  });

  it("renders 1000+ rows without error", () => {
    const data = generateRows(1000);
    renderWithProviders(<ResourceTable data={data} />);
    expect(screen.getByText("1000 resources total")).toBeInTheDocument();
    expect(screen.getByText("Page 1 of 40")).toBeInTheDocument();
  });

  it("filters rows via search", async () => {
    const user = userEvent.setup();
    const data = [
      makeRow({ key: "c1:vm:1", name: "web-server", type: "vm" }),
      makeRow({ key: "c1:ct:1", name: "dns-container", type: "ct" }),
      makeRow({ key: "c1:node:1", name: "node1", type: "node", vmid: null }),
    ];
    renderWithProviders(<ResourceTable data={data} />);

    const searchInput = screen.getByPlaceholderText(/Search/);
    await user.type(searchInput, "type:vm");

    // After typing, only VM should be shown (text includes unfiltered count)
    await waitFor(() => {
      expect(screen.getByText(/1 resource total/)).toBeInTheDocument();
    });
  });

  it("sorts by name column", async () => {
    const user = userEvent.setup();
    const data = [
      makeRow({ key: "c1:vm:1", name: "charlie" }),
      makeRow({ key: "c1:vm:2", name: "alpha" }),
      makeRow({ key: "c1:vm:3", name: "bravo" }),
    ];
    renderWithProviders(<ResourceTable data={data} />);

    const nameHeader = screen.getByRole("button", { name: /Name/ });
    await user.click(nameHeader);

    const rows = screen.getAllByRole("row");
    // First row is header, then sorted data rows
    const firstDataRow = rows[1];
    expect(firstDataRow).toBeDefined();
    if (firstDataRow) {
      expect(within(firstDataRow).getByText("alpha")).toBeInTheDocument();
    }
  });
});

// A dialog one of the row menu's items opens takes what had focus as the
// element to return to — and the menu's own buttons go with the menu.
describe("ResourceTable — the row menu gives focus back", () => {
  const desktop = window.matchMedia.bind(window);
  beforeEach(() => {
    localStorageMock.clear();
    // Below the mobile line, where each row has an Actions button.
    window.matchMedia = (query: string) =>
      Object.assign(desktop(query), { matches: true });
  });
  afterEach(() => {
    window.matchMedia = desktop;
    useVMContextMenuStore.getState().closeDialog();
  });

  it("to the row's Actions button when one of its items is chosen", async () => {
    const user = userEvent.setup();
    renderWithProviders(<ResourceTable data={[makeRow({ key: "c1:vm:0" })]} />);
    const actions = screen.getByRole("button", { name: "Actions for test-vm" });
    await user.click(actions);

    await user.click(screen.getByRole("button", { name: "Clone" }));

    expect(useVMContextMenuStore.getState().openDialog).toBe("clone");
    expect(document.activeElement).toBe(actions);
  });

  it("to what a right click on the row landed on", async () => {
    window.matchMedia = desktop;
    const user = userEvent.setup();
    renderWithProviders(<ResourceTable data={[makeRow({ key: "c1:vm:0" })]} />);
    const name = screen.getByRole("link", { name: "test-vm" });
    fireEvent.contextMenu(name);

    await user.click(screen.getByRole("button", { name: "Clone" }));

    expect(useVMContextMenuStore.getState().openDialog).toBe("clone");
    expect(document.activeElement).toBe(name);
  });

  it("to the row's first control when a right click landed on none", async () => {
    window.matchMedia = desktop;
    const user = userEvent.setup();
    renderWithProviders(<ResourceTable data={[makeRow({ key: "c1:vm:0" })]} />);
    const cluster = screen.getByText("Cluster1");
    const row = cluster.closest("tr");
    if (row === null) throw new Error("no row");
    fireEvent.contextMenu(cluster);

    await user.click(screen.getByRole("button", { name: "Clone" }));

    expect(document.activeElement).toBe(
      within(row).getByRole("checkbox", { name: "Select row" }),
    );
  });

  it("on Escape", async () => {
    window.matchMedia = desktop;
    const user = userEvent.setup();
    renderWithProviders(<ResourceTable data={[makeRow({ key: "c1:vm:0" })]} />);
    const name = screen.getByRole("link", { name: "test-vm" });
    fireEvent.contextMenu(name);

    await user.keyboard("{Escape}");

    expect(
      screen.queryByRole("button", { name: "Clone" }),
    ).not.toBeInTheDocument();
    expect(document.activeElement).toBe(name);
  });

  // A terminal as xterm is one: a mousedown on it focuses its own input and
  // cancels the browser's focusing of what was clicked.
  function Terminal() {
    const input = useRef<HTMLTextAreaElement>(null);
    return (
      <div
        data-testid="terminal"
        onMouseDown={(e) => {
          e.preventDefault();
          input.current?.focus();
        }}
      >
        <textarea ref={input} aria-label="Terminal input" />
      </div>
    );
  }

  it("not after a click outside, which leaves focus where it put it", async () => {
    const user = userEvent.setup();
    renderWithProviders(
      <>
        <ResourceTable data={[makeRow({ key: "c1:vm:0" })]} />
        <Terminal />
      </>,
    );
    await user.click(
      screen.getByRole("button", { name: "Actions for test-vm" }),
    );

    await user.click(screen.getByTestId("terminal"));

    expect(
      screen.queryByRole("button", { name: "Clone" }),
    ).not.toBeInTheDocument();
    expect(document.activeElement).toBe(
      screen.getByRole("textbox", { name: "Terminal input" }),
    );
  });
});
