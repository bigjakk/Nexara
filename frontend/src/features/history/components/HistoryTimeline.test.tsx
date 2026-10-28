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
  // A busy kind starts at its window, without its routine checks and its
  // guests' own activity; the reader can bring each back.
  it.each([
    ["Show routine checks", "exclude_category", "updates", null],
    ["Include guest activity", "include_guests", null, "true"],
  ])(
    "%s brings back what the kind leaves out",
    async (box, param, before, after) => {
      const user = setupUser();
      mockedPage.mockReset();
      mockedPage.mockResolvedValue({ items: [], total: 0 });
      const kind = historyKind(
        "probe",
        { categories: { updates: "Updates", other: "Other" } },
        {
          icons: {},
          defaultWindow: "7d",
          routine: ["updates"],
          guestToggle: true,
        },
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
      expect(first.searchParams.get(param)).toBe(before);
      expect(first.searchParams.get("start_time")).not.toBeNull();

      await user.click(screen.getByRole("checkbox", { name: box }));
      await waitFor(() => {
        expect(mockedPage.mock.calls.length).toBeGreaterThan(1);
      });
      const last = new URL(
        String(mockedPage.mock.calls.at(-1)?.[0]),
        "http://x",
      );
      expect(last.searchParams.get(param)).toBe(after);
    },
  );

  // A capped read's total is one past the cap: "more than" it, and only the
  // cap's worth of pages; a narrowed read has the larger cap, and says so.
  it.each([
    ["unfiltered", "", 101, /^More than 100 entries/, "Page 1 of 2"],
    ["narrowed", "other", 1001, /^More than 1,000 entries/, "Page 1 of 20"],
  ])(
    "reads a total one past the %s cap as more than it",
    async (_name, category, total, more, pages) => {
      const user = setupUser();
      mockedPage.mockReset();
      mockedPage.mockResolvedValue({ items: [], total });
      const kind = historyKind(
        "probe",
        { categories: { other: "Other" } },
        { icons: {}, rowCap: 100, filteredRowCap: 1000 },
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
      if (category) {
        await user.selectOptions(
          screen.getByRole("combobox", { name: "Type" }),
          category,
        );
      }
      expect(await screen.findByText(more)).toBeInTheDocument();
      expect(screen.getByText(pages)).toBeInTheDocument();
      expect(
        screen.queryByText(/narrowed read searches the newest 1,000/) !== null,
      ).toBe(category !== "");
      // Nothing left out, so nothing to bring back.
      expect(screen.queryByRole("checkbox")).toBeNull();
    },
  );

  // Its route reads a kind's default window when none is named, so All time
  // asks for all of it.
  it("asks for all time when the reader picks it", async () => {
    const user = setupUser();
    mockedPage.mockReset();
    mockedPage.mockResolvedValue({ items: [], total: 0 });
    const kind = historyKind(
      "probe",
      { categories: { other: "Other" } },
      { icons: {}, defaultWindow: "7d" },
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
    await user.selectOptions(
      screen.getByRole("combobox", { name: "Time" }),
      "",
    );
    await waitFor(() => {
      expect(mockedPage.mock.calls.length).toBeGreaterThan(1);
    });
    const last = new URL(String(mockedPage.mock.calls.at(-1)?.[0]), "http://x");
    expect(last.searchParams.get("all_time")).toBe("true");
    expect(last.searchParams.get("start_time")).toBeNull();
  });
});
