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

describe("TopConsumers", () => {
  it("renders consumers sorted by CPU by default", () => {
    renderWithProviders(<TopConsumers consumers={consumers} />);
    const list = screen.getByTestId("consumer-list");
    const items = list.querySelectorAll(".space-y-1");
    // First item should be aaaa-1111 (80% CPU), then cccc (60%), then bbbb (40%)
    expect(items[0]?.textContent).toContain("aaaa-111");
    expect(items[1]?.textContent).toContain("cccc-333");
    expect(items[2]?.textContent).toContain("bbbb-222");
  });

  it("sorts by memory when memory button clicked", async () => {
    const user = userEvent.setup();
    renderWithProviders(<TopConsumers consumers={consumers} />);

    await user.click(screen.getByTestId("sort-memory"));

    const list = screen.getByTestId("consumer-list");
    const items = list.querySelectorAll(".space-y-1");
    // Sorted by memory: bbbb (90%), aaaa (50%), cccc (30%)
    expect(items[0]?.textContent).toContain("bbbb-222");
    expect(items[1]?.textContent).toContain("aaaa-111");
    expect(items[2]?.textContent).toContain("cccc-333");
  });

  it("shows empty state when no consumers", () => {
    renderWithProviders(<TopConsumers consumers={[]} />);
    expect(screen.getByTestId("empty-consumers")).toHaveTextContent("No VMs running");
  });

  it("limits display to provided consumers", () => {
    renderWithProviders(<TopConsumers consumers={consumers} />);
    const list = screen.getByTestId("consumer-list");
    const items = list.querySelectorAll(".space-y-1");
    expect(items).toHaveLength(3);
  });
});
