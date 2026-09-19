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
      // A catalogued PATTERN: the payload carries the regex twice — once
      // as `pattern`, once inside the rule — because for a pattern rule
      // they are the same string. The cell must still show one
      // "matches" line.
      rule: {
        name: "pve-configid-existing",
        permits:
          "a PVE configuration id WITHOUT the two-character minimum: a leading letter, then letters, digits, underscore and dash, one character or more.",
        regex: "^[A-Za-z][A-Za-z0-9_-]*$",
      },
      min_length: 2,
      max_length: 40,
      description: "Snapshot name.",
    },
    {
      // A FORMAT whose rule is a regex. Before the rule block, the docs
      // said "format: uuid" and stopped: the regex behind a format was
      // reachable nowhere in this payload, so a caller could not
      // pre-validate and could not predict the 400.
      name: "ident",
      type: "string",
      source: "body",
      optional: true,
      format: "uuid",
      rule: {
        name: "uuid",
        permits:
          "a canonical 8-4-4-4-12 hexadecimal UUID in either case, normalized to lowercase.",
        regex:
          "^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$",
      },
    },
    {
      // The narrowing case, copied from the real POST …/snapshots route:
      // the RULE permits 2 to 128 characters and the PARAMETER caps at 40,
      // because the handler's validateSnapshotName does. Both are
      // published and both apply; the effective contract is 2 to 40.
      name: "configid",
      type: "string",
      source: "body",
      optional: true,
      format: "pve-configid",
      max_length: 40,
      rule: {
        name: "pve-configid",
        permits:
          "a PVE configuration id: a leading letter, then letters, digits, underscore and dash, 2 to 128 characters.",
        regex: "^[A-Za-z][A-Za-z0-9_-]{1,127}$",
      },
    },
    {
      // A rule that validates by PARSING rather than by matching, so it
      // publishes no regex and `permits` is the whole statement.
      name: "size",
      type: "string",
      source: "body",
      optional: true,
      format: "disk-size",
      rule: {
        name: "disk-size",
        permits:
          "a decimal size with an optional binary unit, normalized to a whole GiB count between 1 and 1048576.",
      },
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
        rule: {
          name: "pve-object-id",
          permits:
            "a PVE object id: a leading letter or digit, then letters, digits, dot, underscore and dash.",
          regex: "^[A-Za-z0-9][A-Za-z0-9._-]*$",
        },
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

    it("says what a format PERMITS, not just that one applies", () => {
      // The whole point of the change. `format: uuid` names a rule and
      // declines to say what it is; an operator on this page could go and
      // read the server's source, and an external consumer could not.
      renderRow(constrained, true);
      const table = within(screen.getByRole("table"));
      expect(table.getByText("format: uuid")).toBeInTheDocument();
      expect(
        table.getByText(
          "permits a canonical 8-4-4-4-12 hexadecimal UUID in either case, normalized to lowercase.",
        ),
      ).toBeInTheDocument();
      // A format's regex lives nowhere else in the payload, so this line
      // exists only because the rule carries it.
      expect(
        table.getByText(
          "matches ^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$",
        ),
      ).toBeInTheDocument();
    });

    it("names a pattern's rule and shows one matches line, not two", () => {
      renderRow(constrained, true);
      const table = within(screen.getByRole("table"));
      // A pattern carries no format, so the rule's NAME is the only handle
      // a reader has on it.
      expect(
        table.getByText("rule: pve-configid-existing"),
      ).toBeInTheDocument();
      expect(
        table.getByText(/^permits a PVE configuration id WITHOUT/),
      ).toBeInTheDocument();
      // `pattern` and `rule.regex` are the same string for a pattern rule;
      // rendering both would put the regex on screen twice.
      expect(
        table.getAllByText("matches ^[A-Za-z][A-Za-z0-9_-]*$"),
      ).toHaveLength(1);
    });

    it("shows no regex for a rule that validates by parsing", () => {
      renderRow(constrained, true);
      const table = within(screen.getByRole("table"));
      const row = table.getByText("size").closest("tr");
      expect(row).not.toBeNull();
      const cells = within(row as HTMLElement);
      expect(cells.getByText("format: disk-size")).toBeInTheDocument();
      expect(cells.getByText(/^permits a decimal size/)).toBeInTheDocument();
      // disk-size parses and converts; there is no regex to compile, and a
      // "matches" line here would offer one that does not exist.
      expect(cells.queryByText(/^matches /)).not.toBeInTheDocument();
    });

    it("says what an array element's rule permits", () => {
      renderRow(constrained, true);
      const table = within(screen.getByRole("table"));
      expect(table.getByText("rule: pve-object-id")).toBeInTheDocument();
      expect(table.getByText(/^permits a PVE object id/)).toBeInTheDocument();
      expect(
        table.getByText("matches ^[A-Za-z0-9][A-Za-z0-9._-]*$"),
      ).toBeInTheDocument();
    });

    it("carries the rule into the small-width list too", () => {
      const { container } = renderRow(constrained, true);
      const dl = within(container.querySelector("dl") as HTMLElement);
      expect(dl.getByText("format: uuid")).toBeInTheDocument();
      expect(
        dl.getByText(
          "permits a canonical 8-4-4-4-12 hexadecimal UUID in either case, normalized to lowercase.",
        ),
      ).toBeInTheDocument();
      expect(dl.getByText("rule: pve-configid-existing")).toBeInTheDocument();
    });

    it("shows no rule line for a parameter that names none", () => {
      renderRow(constrained, true);
      const table = within(screen.getByRole("table"));
      const row = table.getByText("unbounded").closest("tr");
      expect(row).not.toBeNull();
      const cells = within(row as HTMLElement);
      expect(cells.queryByText(/^permits /)).not.toBeInTheDocument();
      expect(cells.queryByText(/^rule: /)).not.toBeInTheDocument();
    });

    it("does not let a narrower parameter read as a contradiction of its rule", () => {
      // The live failure this guards. `up to 40 chars` and
      // `permits … 2 to 128 characters.` are both true and both published:
      // the first is the PARAMETER's cap, the second is what the named
      // RULE allows in general, and a request must satisfy both. Rendered
      // as two adjacent lines in one flat list they read as one correcting
      // the other, and a caller cannot tell which governs.
      renderRow(constrained, true);
      const row = within(screen.getByRole("table"))
        .getByText("configid")
        .closest("tr");
      expect(row).not.toBeNull();
      const cells = within(row as HTMLElement);

      const cap = cells.getByText("up to 40 chars");
      const permits = cells.getByText(/^permits a PVE configuration id/);
      expect(cap).toBeInTheDocument();
      expect(permits).toBeInTheDocument();

      // The fix is STRUCTURAL, not a matter of wording: the rule's lines
      // live inside their own indented scope and the parameter's do not.
      // Asserting on the text alone would pass just as happily against the
      // flat list that caused the problem.
      expect(permits.closest(".border-l")).not.toBeNull();
      expect(cap.closest(".border-l")).toBeNull();

      // And the scope is named, so a reader knows whose 2-to-128 it is.
      expect(cells.getByText("format: pve-configid")).toBeInTheDocument();
    });

    it("says outright that the parameter's own limits apply as well", () => {
      renderRow(constrained, true);
      const row = within(screen.getByRole("table"))
        .getByText("configid")
        .closest("tr");
      const cells = within(row as HTMLElement);
      expect(
        cells.getByText(/and the limits above apply as well/),
      ).toBeInTheDocument();
    });

    it("omits that line for a parameter that adds no limits of its own", () => {
      // `ident` is a bare uuid format with no bounds, so there is nothing
      // for the rule to combine with and the line would be noise.
      renderRow(constrained, true);
      const row = within(screen.getByRole("table"))
        .getByText("ident")
        .closest("tr");
      const cells = within(row as HTMLElement);
      expect(
        cells.queryByText(/and the limits above apply as well/),
      ).not.toBeInTheDocument();
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
