import { beforeEach, describe, it, expect, vi } from "vitest";
import { renderWithProviders } from "@/test/test-utils";
import { DashboardGrid } from "./DashboardGrid";
import type { DashboardPreset } from "../lib/widget-registry";
import type { Layout, LayoutItem } from "react-grid-layout";

// Capture the onLayoutChange callback that react-grid-layout would normally call.
let capturedOnLayoutChange:
  | ((layout: Layout, layouts: Partial<Record<string, Layout>>) => void)
  | null = null;
// The grid's container width, which picks the breakpoint.
let mockWidth = 1200;

vi.mock("react-grid-layout", async () => {
  const React = await import("react");
  const actual =
    await vi.importActual<typeof import("react-grid-layout")>(
      "react-grid-layout",
    );
  return {
    getBreakpointFromWidth: actual.getBreakpointFromWidth,
    ResponsiveGridLayout: ({
      children,
      onLayoutChange,
    }: {
      children: React.ReactNode;
      onLayoutChange: (
        layout: Layout,
        layouts: Partial<Record<string, Layout>>,
      ) => void;
    }) => {
      capturedOnLayoutChange = onLayoutChange;
      return React.createElement("div", { "data-testid": "grid" }, children);
    },
    useContainerWidth: () => ({
      width: mockWidth,
      mounted: true,
      containerRef: { current: null },
      measureWidth: () => undefined,
    }),
  };
});

const originalLgLayout: LayoutItem[] = [
  { i: "stats-overview", x: 0, y: 0, w: 12, h: 3 },
  { i: "cluster-cards", x: 0, y: 3, w: 12, h: 4 },
];

const preset: DashboardPreset = {
  name: "Default",
  widgetIds: ["stats-overview", "cluster-cards"],
  layouts: originalLgLayout,
};

describe("DashboardGrid breakpoint handling", () => {
  beforeEach(() => {
    mockWidth = 1200;
  });

  // 996 is md's own threshold, which react-grid-layout still lays out as sm:
  // it takes a breakpoint only above its width.
  it.each([700, 996])(
    "preserves the lg layout when a breakpoint change fires onLayoutChange with a smaller-breakpoint layout (%i px)",
    (width) => {
      mockWidth = width;
      const onLayoutChange = vi.fn();
      const onReset = vi.fn();

      renderWithProviders(
        <DashboardGrid
          preset={preset}
          defaultPreset={preset}
          clusters={[]}
          clusterNames={new Map()}
          onLayoutChange={onLayoutChange}
          onReset={onReset}
          editMode
        >
          {(id) => <div key={id}>{id}</div>}
        </DashboardGrid>,
      );

      expect(capturedOnLayoutChange).not.toBeNull();

      // Simulate the library firing onLayoutChange after a viewport shrink
      // from lg to sm. args[0] is the sm-fitted layout (compacted to 6 cols);
      // args[1].lg is preserved unchanged across the breakpoint transition.
      const smCompactedLayout: LayoutItem[] = [
        { i: "stats-overview", x: 0, y: 0, w: 6, h: 3 },
        { i: "cluster-cards", x: 0, y: 3, w: 6, h: 4 },
      ];

      capturedOnLayoutChange?.(smCompactedLayout, {
        lg: originalLgLayout,
        sm: smCompactedLayout,
      });

      // Parent should be notified with the ORIGINAL lg layout, not the
      // sm-compacted one. Otherwise the lg layout in storage gets overwritten
      // with sm dimensions and the dashboard "sticks" small after the window
      // grows again.
      expect(onLayoutChange).toHaveBeenCalledTimes(1);
      const savedLayout = onLayoutChange.mock.calls[0]?.[0] as
        Layout | undefined;
      expect(savedLayout).toEqual(originalLgLayout);
    },
  );

  it("saves the new layout when the user drags at lg (args[1].lg actually changed)", () => {
    const onLayoutChange = vi.fn();
    const onReset = vi.fn();

    renderWithProviders(
      <DashboardGrid
        preset={preset}
        defaultPreset={preset}
        clusters={[]}
        clusterNames={new Map()}
        onLayoutChange={onLayoutChange}
        onReset={onReset}
        editMode
      >
        {(id) => <div key={id}>{id}</div>}
      </DashboardGrid>,
    );

    const draggedLgLayout: LayoutItem[] = [
      { i: "stats-overview", x: 6, y: 0, w: 6, h: 3 },
      { i: "cluster-cards", x: 0, y: 3, w: 12, h: 4 },
    ];

    capturedOnLayoutChange?.(draggedLgLayout, { lg: draggedLgLayout });

    expect(onLayoutChange).toHaveBeenCalledTimes(1);
    const savedLayout = onLayoutChange.mock.calls[0]?.[0] as Layout | undefined;
    expect(savedLayout).toEqual(draggedLgLayout);
  });

  // md has lg's 12 columns: a 1440 px window with the tree open lands there,
  // and keeping only args[1].lg dropped every drag and resize made at it.
  it("saves a drag made at md, which has lg's 12 columns", () => {
    mockWidth = 1076;
    const onLayoutChange = vi.fn();

    renderWithProviders(
      <DashboardGrid
        preset={preset}
        defaultPreset={preset}
        clusters={[]}
        clusterNames={new Map()}
        onLayoutChange={onLayoutChange}
        onReset={vi.fn()}
        editMode
      >
        {(id) => <div key={id}>{id}</div>}
      </DashboardGrid>,
    );

    const draggedMdLayout: LayoutItem[] = [
      { i: "stats-overview", x: 4, y: 0, w: 8, h: 6 },
      { i: "cluster-cards", x: 0, y: 6, w: 12, h: 4 },
    ];

    capturedOnLayoutChange?.(draggedMdLayout, {
      lg: originalLgLayout,
      md: draggedMdLayout,
    });

    expect(onLayoutChange.mock.calls[0]?.[0]).toEqual(draggedMdLayout);
  });
});
