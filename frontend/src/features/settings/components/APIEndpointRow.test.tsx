import { describe, it, expect, vi } from "vitest";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import type { APIEndpoint } from "@/types/api";
import { APIEndpointRow } from "./APIEndpointRow";

/**
 * The declared endpoint these tests read is the disk-attach one, because its
 * `index` parameter is the reason this page shows parameters at all: optional
 * with NO default, so omitting it takes the lowest free slot rather than slot
 * 0 — which on a VM with a disk is its boot disk.
 */
const declared: APIEndpoint = {
  method: "POST",
  path: "/api/v1/clusters/:cluster_id/vms/:vm_id/disks/attach",
  description: "Allocate a new disk and attach it.",
  permission: "manage:vm",
  group: "Virtual Machines",
  parameters: [
    {
      name: "cluster_id",
      type: "string",
      source: "path",
      optional: false,
      format: "uuid",
      typetext: "<uuid>",
      description: "Nexara cluster identifier.",
    },
    {
      name: "bus",
      type: "string",
      source: "body",
      optional: false,
      enum: ["scsi", "sata", "virtio", "ide"],
      description: "Controller to attach the disk to.",
    },
    {
      name: "index",
      type: "integer",
      source: "body",
      optional: true,
      description: "Slot on the bus. Omit to take the lowest free one.",
    },
    {
      name: "vmstate",
      type: "boolean",
      source: "body",
      optional: true,
      default: false,
      description: "Include the running guest's RAM.",
    },
    {
      name: "retries",
      type: "integer",
      source: "query",
      optional: true,
      default: 0,
      description: "How many times to retry.",
    },
    {
      name: "format",
      type: "string",
      source: "body",
      optional: true,
      requires: ["bus"],
      description: "Image format.",
    },
  ],
};

/**
 * A second declared endpoint, carrying the constraint facets. `floor` is
 * the one that matters most: a minimum of 0 is a real bound, and a
 * truthiness check would drop it exactly as `if (p.default)` would drop a
 * default of 0.
 */
const constrained: APIEndpoint = {
  method: "POST",
  path: "/api/v1/probe",
  description: "Probe.",
  permission: "manage:probe",
  group: "Probe",
  parameters: [
    {
      name: "snap_name",
      type: "string",
      source: "body",
      optional: false,
      pattern: "^[A-Za-z][A-Za-z0-9_-]*$",
      min_length: 2,
      max_length: 40,
      description: "Snapshot name.",
    },
    {
      name: "floor",
      type: "integer",
      source: "body",
      optional: true,
      minimum: 0,
      maximum: 30,
    },
    {
      name: "blank",
      type: "string",
      source: "body",
      optional: true,
      max_length: 0,
    },
    {
      name: "ceiling",
      type: "integer",
      source: "body",
      optional: true,
      maximum: 0,
    },
    { name: "unbounded", type: "string", source: "body", optional: true },
    {
      name: "newname",
      type: "string",
      source: "body",
      optional: true,
      alias: "oldname",
    },
    {
      name: "tags",
      type: "array",
      source: "body",
      optional: true,
      items: {
        type: "string",
        enum: ["red", "green"],
        max_length: 16,
        typetext: "<colour>",
        description: "A colour tag.",
      },
    },
    {
      // Length bounds on an ARRAY count elements, not characters — the
      // server's own rejection says "must have at least N items".
      name: "nics",
      type: "array",
      source: "body",
      optional: true,
      min_length: 1,
      max_length: 8,
      items: { type: "string" },
    },
  ],
};

/** A route the server has not migrated: prose only, no schema. */
const legacy: APIEndpoint = {
  method: "GET",
  path: "/api/v1/audit-log",
  description: "List audit log entries",
  permission: "view:audit",
  group: "Audit Log",
};

function renderRow(endpoint: APIEndpoint, expanded = false) {
  const onToggle = vi.fn();
  const result = render(
    <APIEndpointRow
      endpoint={endpoint}
      expanded={expanded}
      onToggle={onToggle}
    />,
  );
  return { ...result, onToggle };
}

describe("APIEndpointRow", () => {
  it("renders the summary line for any endpoint", () => {
    renderRow(declared);
    expect(screen.getByText("POST")).toBeInTheDocument();
    expect(screen.getByText(declared.path)).toBeInTheDocument();
    expect(screen.getByText(declared.description)).toBeInTheDocument();
    expect(screen.getByText("manage:vm")).toBeInTheDocument();
  });

  it("offers no expand affordance for a legacy endpoint, and no placeholder", () => {
    const { container } = renderRow(legacy);
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
    expect(container.querySelector("table")).toBeNull();
    // Absence is the honest rendering — an endpoint with no published schema
    // must not be shown as one that takes no parameters.
    expect(screen.queryByText(/no parameters/i)).not.toBeInTheDocument();
  });

  it("toggles on click and reports its state to assistive tech", async () => {
    const user = userEvent.setup();
    const { onToggle } = renderRow(declared);

    const button = screen.getByRole("button");
    expect(button).toHaveAttribute("aria-expanded", "false");
    await user.click(button);
    expect(onToggle).toHaveBeenCalledTimes(1);
  });

  it("shows nothing until expanded", () => {
    const { container } = renderRow(declared, false);
    expect(container.querySelector("table")).toBeNull();
    expect(container.querySelector("dl")).toBeNull();
  });

  it("renders every parameter in the table once expanded", () => {
    renderRow(declared, true);
    const table = within(screen.getByRole("table"));
    for (const param of declared.parameters ?? []) {
      expect(table.getByText(param.name)).toBeInTheDocument();
    }
    expect(screen.getByRole("button")).toHaveAttribute("aria-expanded", "true");
  });

  it("keeps optional-with-no-default apart from optional-with-a-default", () => {
    renderRow(declared, true);
    const table = within(screen.getByRole("table"));

    // Required.
    expect(table.getAllByText("Required")).toHaveLength(2);
    // Optional, and the endpoint decides — the index case.
    expect(table.getAllByText("Optional")).toHaveLength(2);
    // Optional with a FALSY default: both must still be shown, or the row
    // reads as "the endpoint decides" when in fact it does not.
    expect(table.getByText("Default false")).toBeInTheDocument();
    expect(table.getByText("Default 0")).toBeInTheDocument();
  });

  it("names the source of every parameter", () => {
    renderRow(declared, true);
    const table = within(screen.getByRole("table"));
    expect(table.getAllByText("path")).toHaveLength(1);
    expect(table.getAllByText("query")).toHaveLength(1);
    expect(table.getAllByText("body")).toHaveLength(4);
  });

  it("shows enum values, formats and companion requirements", () => {
    renderRow(declared, true);
    const table = within(screen.getByRole("table"));
    expect(table.getByText("scsi")).toBeInTheDocument();
    expect(table.getByText("virtio")).toBeInTheDocument();
    expect(table.getByText("format: uuid")).toBeInTheDocument();
    expect(table.getByText("send with bus")).toBeInTheDocument();
  });

  describe("constraints", () => {
    it("spells out a pattern and a length range", () => {
      renderRow(constrained, true);
      const table = within(screen.getByRole("table"));
      // The snap_name case: a caller sending "1abc" gets a 400, and this
      // is the only thing in the docs that lets them predict it.
      expect(
        table.getByText("matches ^[A-Za-z][A-Za-z0-9_-]*$"),
      ).toBeInTheDocument();
      expect(table.getByText("2–40 chars")).toBeInTheDocument();
    });

    it("renders a ZERO bound rather than dropping it", () => {
      renderRow(constrained, true);
      const table = within(screen.getByRole("table"));
      // minimum 0 with maximum 30 — a truthiness check would render
      // "max 30" and lose the floor entirely.
      expect(table.getByText("0 to 30")).toBeInTheDocument();
      expect(table.getByText("max 0")).toBeInTheDocument();
      expect(table.getByText("up to 0 chars")).toBeInTheDocument();
    });

    it("shows nothing for a parameter that declares no constraints", () => {
      renderRow(constrained, true);
      const table = within(screen.getByRole("table"));
      const row = table.getByText("unbounded").closest("tr");
      expect(row).not.toBeNull();
      const cells = within(row as HTMLElement);
      expect(cells.queryByText(/^min /)).not.toBeInTheDocument();
      expect(cells.queryByText(/^max /)).not.toBeInTheDocument();
      expect(cells.queryByText(/chars$/)).not.toBeInTheDocument();
      expect(cells.queryByText(/^matches /)).not.toBeInTheDocument();
    });

    it("shows a second accepted name for an aliased parameter", () => {
      renderRow(constrained, true);
      const table = within(screen.getByRole("table"));
      expect(table.getByText("or oldname")).toBeInTheDocument();
    });

    it("says what goes inside an array", () => {
      renderRow(constrained, true);
      const table = within(screen.getByRole("table"));
      expect(table.getAllByText("each item:")).toHaveLength(2);
      expect(table.getByText("red")).toBeInTheDocument();
      expect(table.getByText("green")).toBeInTheDocument();
      expect(table.getByText("up to 16 chars")).toBeInTheDocument();
      // Every facet the server publishes about an element, not just the
      // three that happened to be asserted first.
      expect(table.getByText("<colour>")).toBeInTheDocument();
      expect(table.getByText("A colour tag.")).toBeInTheDocument();
    });

    it("counts array length in items, not characters", () => {
      renderRow(constrained, true);
      const table = within(screen.getByRole("table"));
      expect(table.getByText("1–8 items")).toBeInTheDocument();
      expect(table.queryByText("1–8 chars")).not.toBeInTheDocument();
    });

    it("carries the same constraints into the small-width list", () => {
      const { container } = renderRow(constrained, true);
      const dl = within(container.querySelector("dl") as HTMLElement);
      expect(dl.getByText("0 to 30")).toBeInTheDocument();
      expect(dl.getByText("or oldname")).toBeInTheDocument();
      expect(
        dl.getByText("matches ^[A-Za-z][A-Za-z0-9_-]*$"),
      ).toBeInTheDocument();
    });

    it("keeps the description a separate node from the type in the list", () => {
      // The JSX-whitespace trap: a bare {param.description} beside
      // ParameterShape's leading inline <span> renders
      // "Snapshot name.string" with no space, on every phone-width view
      // of every migrated endpoint.
      //
      // The assertion is STRUCTURAL, and both of the obvious
      // alternatives are blind to it. textContent concatenates across
      // element boundaries with no separator, so it reads
      // "Snapshot name.string" whether or not the fix is in place. And
      // Testing Library's getNodeText concatenates only a node's DIRECT
      // text children, so a bare getByText("Snapshot name.") matches the
      // glued <dd> just as happily as the fixed <div>.
      //
      // What actually distinguishes them is WHICH element owns the text:
      // its own block, or the <dd> itself with the type's inline <span>
      // hard up against it.
      const { container } = renderRow(constrained, true);
      const dd = container.querySelector("dl dd");
      expect(dd).not.toBeNull();

      const owner = within(dd as HTMLElement).getByText("Snapshot name.");
      expect(owner.tagName).toBe("DIV");

      const looseText = Array.from((dd as HTMLElement).childNodes).filter(
        (n) =>
          n.nodeType === Node.TEXT_NODE && (n.textContent ?? "").trim() !== "",
      );
      expect(looseText).toHaveLength(0);
    });
  });

  it("also renders a definition list, for widths a table cannot use", () => {
    // Both are in the DOM; Tailwind's md: breakpoint decides which is
    // visible, and jsdom applies no stylesheet. The assertion is that the
    // small-width path exists and carries the same three-state labels — not
    // which one a given viewport shows.
    const { container } = renderRow(declared, true);
    const list = container.querySelector("dl");
    expect(list).not.toBeNull();
    const dl = within(list as HTMLElement);
    expect(dl.getByText("index")).toBeInTheDocument();
    expect(dl.getByText("Default false")).toBeInTheDocument();
    expect(dl.getAllByText("Optional")).toHaveLength(2);
  });
});
