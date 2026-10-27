import { describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { setupUser } from "@/test/user";
import { apiClient } from "@/lib/api-client";
import { apiPath } from "@/lib/api-path";
import { historyKind } from "../lib/history";
import { HistoryTimeline } from "./HistoryTimeline";

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

const mockedPage = vi.mocked(apiClient.page);

describe("HistoryTimeline", () => {
  // A busy kind starts at its window and without its routine checks; the
  // reader can ask for those.
  it("reads a kind's default window without its routine categories, until asked", async () => {
    const user = setupUser();
    mockedPage.mockReset();
    mockedPage.mockResolvedValue({ items: [], total: 0 });
    const kind = historyKind(
      "probe",
      { categories: { updates: "Updates", other: "Other" } },
      { icons: {}, defaultWindow: "7d", routine: ["updates"] },
    );
    renderWithProviders(
      <HistoryTimeline
        kind={kind}
        target={{
          clusterId: "c1",
          path: (query) => apiPath`/api/v1/probes/p1/history?${query}`,
        }}
      />,
    );
    await waitFor(() => {
      expect(mockedPage).toHaveBeenCalled();
    });
    const first = new URL(String(mockedPage.mock.calls[0]?.[0]), "http://x");
    expect(first.searchParams.get("exclude_category")).toBe("updates");
    expect(first.searchParams.get("start_time")).not.toBeNull();

    await user.click(
      screen.getByRole("checkbox", { name: "Show routine checks" }),
    );
    await waitFor(() => {
      expect(mockedPage.mock.calls.length).toBeGreaterThan(1);
    });
    const last = new URL(String(mockedPage.mock.calls.at(-1)?.[0]), "http://x");
    expect(last.searchParams.get("exclude_category")).toBeNull();
  });

  // A capped read's total is one past the cap: "more than" it, and only the
  // cap's worth of pages.
  it("reads a total one past the kind's cap as more than it", async () => {
    mockedPage.mockReset();
    mockedPage.mockResolvedValue({ items: [], total: 101 });
    const kind = historyKind(
      "probe",
      { categories: { other: "Other" } },
      { icons: {}, rowCap: 100 },
    );
    renderWithProviders(
      <HistoryTimeline
        kind={kind}
        target={{
          clusterId: "c1",
          path: (query) => apiPath`/api/v1/probes/p1/history?${query}`,
        }}
      />,
    );
    expect(
      await screen.findByText(/^More than 100 entries/),
    ).toBeInTheDocument();
    expect(screen.getByText("Page 1 of 2")).toBeInTheDocument();
  });
});
