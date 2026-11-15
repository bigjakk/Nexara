import { describe, expect, it } from "vitest";
import { screen } from "@testing-library/react";
import { makeVM } from "@/test/cluster.fixtures";
import { renderWithProviders } from "@/test/test-utils";
import { TreeGuestRow } from "./TreeRow";

describe("TreeGuestRow", () => {
  it("dots a halted VM by its QEMU state, not as running", () => {
    renderWithProviders(
      <TreeGuestRow
        vm={makeVM({ status: "running", run_state: "guest-panicked" })}
        clusterId="c1"
        currentNode="pve-01"
        depth={1}
      />,
    );
    expect(screen.getByRole("img", { name: "Panicked" })).toBeInTheDocument();
  });
});
