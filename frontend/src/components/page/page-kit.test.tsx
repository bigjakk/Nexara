import { describe, it, expect, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/test-utils";
import { Segmented } from "@/components/ui/segmented";
import { usageLevel } from "@/lib/usage-level";
import { DetailHeader, Fact, StatusFact } from "./DetailHeader";
import { Stat, StatStrip } from "./StatStrip";

describe("page kit", () => {
  it("DetailHeader links each ancestor and names the page once, as its heading", () => {
    renderWithProviders(
      <DetailHeader
        crumbs={[
          { label: "Inventory", to: "/inventory" },
          { label: "cluster01", to: "/clusters/c1" },
          { label: "linux01" },
        ]}
        icon={null}
        title="linux01"
        facts={
          <>
            <StatusFact status="running" label="Running" detail="for 3d" />
            <Fact label="VM">101</Fact>
          </>
        }
      />,
    );
    expect(screen.getByRole("link", { name: "cluster01" })).toHaveAttribute(
      "href",
      "/clusters/c1",
    );
    // The page itself is in the trail as text, not as a link to itself.
    expect(screen.queryByRole("link", { name: "linux01" })).toBeNull();
    expect(screen.getByText("linux01", { selector: "[aria-current]" }));
    expect(screen.getByRole("heading", { level: 1, name: "linux01" }));
    expect(screen.getByText("101")).toBeInTheDocument();
  });

  it("a Stat with onSelect is a toggle that reports which measure is picked", async () => {
    const onSelect = vi.fn();
    renderWithProviders(
      <StatStrip>
        <Stat label="CPU" value="4%" selected onSelect={vi.fn()} />
        <Stat label="Memory" value="62%" onSelect={onSelect} />
        <Stat label="Uptime" value="3d" />
      </StatStrip>,
    );
    expect(screen.getByRole("button", { name: /CPU/ })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    await userEvent.click(screen.getByRole("button", { name: /Memory/ }));
    expect(onSelect).toHaveBeenCalledOnce();
    // A plain reading is not a control.
    expect(screen.queryByRole("button", { name: /Uptime/ })).toBeNull();
  });

  it("Segmented marks the current option and reports a change, not a re-press", async () => {
    const onValueChange = vi.fn();
    renderWithProviders(
      <Segmented
        aria-label="Range"
        value="1h"
        onValueChange={onValueChange}
        options={[
          { value: "1h", label: "1h" },
          { value: "24h", label: "24h" },
        ]}
      />,
    );
    const group = screen.getByRole("group", { name: "Range" });
    expect(group).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "1h" }));
    expect(onValueChange).not.toHaveBeenCalled();
    await userEvent.click(screen.getByRole("button", { name: "24h" }));
    expect(onValueChange).toHaveBeenCalledWith("24h");
  });

  it.each([
    [null, "ok"],
    [74.9, "ok"],
    [75, "warn"],
    [89.9, "warn"],
    [90, "err"],
  ] as const)("usageLevel(%s) is %s", (percent, level) => {
    expect(usageLevel(percent)).toBe(level);
  });
});
