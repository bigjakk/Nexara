import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { NodeCheckResult } from "./NodeCheckResult";

describe("NodeCheckResult", () => {
  it("shows why a node was not checked, what Proxmox reported, or OK", () => {
    const { rerender } = render(
      <NodeCheckResult
        node="pve-01"
        unchecked={{ "pve-01": "The node is offline." }}
        checks={{}}
      />,
    );
    expect(
      screen.getByText("Not checked: The node is offline."),
    ).toBeInTheDocument();

    rerender(
      <NodeCheckResult
        node="pve-01"
        unchecked={{}}
        checks={{
          "pve-01": [{ severity: "error", message: "Invalid configuration" }],
        }}
      />,
    );
    expect(screen.getByText("Invalid configuration")).toBeInTheDocument();

    rerender(
      <NodeCheckResult
        node="pve-01"
        unchecked={{}}
        checks={{ "pve-01": [] }}
      />,
    );
    expect(screen.getByText("OK")).toBeInTheDocument();

    rerender(
      <NodeCheckResult
        node="pve-01"
        unchecked={{}}
        checks={{ "pve-01": [] }}
        showOK={false}
      />,
    );
    expect(screen.queryByText("OK")).not.toBeInTheDocument();
  });

  // The maps are parsed JSON: a node named after an inherited property has
  // nothing in them unless the listing sent it.
  it.each(["constructor", "toString", "hasOwnProperty", "__proto__"])(
    "reads a node named %s as not listed, not as what every object inherits",
    (node) => {
      const parsed = JSON.parse('{"unchecked":{},"node_checks":{}}') as {
        unchecked: Record<string, string>;
        node_checks: Record<string, []>;
      };
      render(
        <NodeCheckResult
          node={node}
          unchecked={parsed.unchecked}
          checks={parsed.node_checks}
        />,
      );
      expect(screen.getByText("Not checked.")).toBeInTheDocument();
    },
  );

  it("still reads such a node when the listing did send it", () => {
    const parsed = JSON.parse(
      '{"unchecked":{},"node_checks":{"constructor":[]}}',
    ) as { unchecked: Record<string, string>; node_checks: Record<string, []> };
    render(
      <NodeCheckResult
        node="constructor"
        unchecked={parsed.unchecked}
        checks={parsed.node_checks}
      />,
    );
    expect(screen.getByText("OK")).toBeInTheDocument();
  });
});
