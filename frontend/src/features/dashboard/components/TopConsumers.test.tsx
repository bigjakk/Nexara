import { describe, it, expect } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/test-utils";
import { TopConsumers } from "./TopConsumers";
import type { TopConsumer } from "@/types/ws";

const consumers: TopConsumer[] = [
  { vmId: "aaaa-1111", cpuPercent: 80, memPercent: 50, memUsed: 2e9, memTotal: 4e9 },
  { vmId: "bbbb-2222", cpuPercent: 40, memPercent: 90, memUsed: 3.6e9, memTotal: 4e9 },
  { vmId: "cccc-3333", cpuPercent: 60, memPercent: 30, memUsed: 1.2e9, memTotal: 4e9 },
];

const vmNames = new Map<string, string>([
  ["aaaa-1111", "web-01"],
  ["bbbb-2222", "db-01"],
  ["cccc-3333", "cache-01"],
]);

describe("TopConsumers", () => {
  it("renders consumers sorted by CPU by default", () => {
    renderWithProviders(<TopConsumers consumers={consumers} vmNames={vmNames} />);
    const list = screen.getByTestId("consumer-list");
    const items = list.querySelectorAll(".space-y-1");
    // First item should be web-01 (80% CPU), then cache-01 (60%), then db-01 (40%)
    expect(items[0]?.textContent).toContain("web-01");
    expect(items[1]?.textContent).toContain("cache-01");
    expect(items[2]?.textContent).toContain("db-01");
  });

  it("sorts by memory when memory button clicked", async () => {
    const user = userEvent.setup();
    renderWithProviders(<TopConsumers consumers={consumers} vmNames={vmNames} />);

    await user.click(screen.getByTestId("sort-memory"));

    const list = screen.getByTestId("consumer-list");
    const items = list.querySelectorAll(".space-y-1");
    // Sorted by memory: db-01 (90%), web-01 (50%), cache-01 (30%)
    expect(items[0]?.textContent).toContain("db-01");
    expect(items[1]?.textContent).toContain("web-01");
    expect(items[2]?.textContent).toContain("cache-01");
  });

  it("shows empty state when no consumers", () => {
    renderWithProviders(<TopConsumers consumers={[]} vmNames={new Map()} />);
    expect(screen.getByTestId("empty-consumers")).toHaveTextContent("No VMs running");
  });

  it("limits display to provided consumers", () => {
    renderWithProviders(<TopConsumers consumers={consumers} vmNames={vmNames} />);
    const list = screen.getByTestId("consumer-list");
    const items = list.querySelectorAll(".space-y-1");
    expect(items).toHaveLength(3);
  });
});
