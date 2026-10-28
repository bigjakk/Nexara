import { describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { setupUser } from "@/test/user";
import { apiClient } from "@/lib/api-client";
import { apiPath } from "@/lib/api-path";
import { formatDateTime } from "@/lib/format";
import { makeHistoryEntry } from "@/test/history.fixtures";
import { historyKind } from "../lib/history";
import type { HistoryPage, HistoryTarget } from "../types/history";
import { HistoryTimeline } from "./HistoryTimeline";

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

const mockedPage = vi.mocked(apiClient.page);

const probeTarget: HistoryTarget = {
  clusterId: "c1",
  path: (query) => apiPath`/api/v1/probes/p1/history?${query}`,
};

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
      renderWithProviders(<HistoryTimeline kind={kind} target={probeTarget} />);
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

  // A read whole back to a point says so, and that narrowing searches deeper
  // where it does. Show older reads below the point with end_time, a stretch
  // a page at a time and then the stretch below it, and appends each entry
  // once; the page it started from is held still.
  it("shows older entries below where a read was whole, each once", async () => {
    const user = setupUser();
    mockedPage.mockReset();
    const s1 = "2026-01-02T03:04:05.000001Z";
    const s2 = "2026-01-01T01:00:00.000001Z";
    const entry = (id: string, action: string) =>
      makeHistoryEntry({ id, action, resource_type: "node" });
    const migrations = Array.from({ length: 49 }, (_, i) =>
      entry(`audit:m${String(i)}`, "migrate"),
    );
    // By end_time and offset: the base page; the stretch below it, 51 long,
    // whose first page repeats one of the base's; and the last stretch.
    const pages: Record<string, HistoryPage> = {
      ":0": {
        items: [entry("audit:3", "reboot"), entry("audit:2", "shutdown")],
        total: 2,
        searched_from: s1,
      },
      [`${s1}:0`]: {
        items: [entry("audit:2", "shutdown"), ...migrations],
        total: 51,
        searched_from: s2,
      },
      [`${s1}:50`]: {
        items: [entry("audit:1", "wakeonlan")],
        total: 51,
        searched_from: s2,
      },
      [`${s2}:0`]: { items: [entry("audit:0", "hibernate")], total: 1 },
    };
    mockedPage.mockImplementation((path) => {
      const q = new URL(String(path), "http://x").searchParams;
      const at = `${q.get("end_time") ?? ""}:${q.get("offset") ?? ""}`;
      return Promise.resolve(pages[at] ?? { items: [], total: 0 });
    });
    const kind = historyKind(
      "probe",
      { categories: { other: "Other" } },
      { icons: {}, rowCap: 100, filteredRowCap: 1000 },
    );
    const { queryClient } = renderWithProviders(
      <HistoryTimeline kind={kind} target={probeTarget} />,
    );
    // The note, with the hint a read that is not narrowed carries.
    const note = (when: string) =>
      `Complete back to ${formatDateTime(when)}; Show older lists the entries before it. ` +
      "A read narrowed by type or source searches the newest 1,000 entries of each kind of record instead of 100.";
    expect(await screen.findByText(note(s1))).toBeInTheDocument();
    const showOlder = () =>
      user.click(screen.getByRole("button", { name: "Show older" }));

    await showOlder();
    expect(await screen.findAllByText("Migrate")).toHaveLength(49);
    // Mid-stretch, the listing is whole back to the stretch's top.
    expect(screen.getByText(note(s1))).toBeInTheDocument();
    await showOlder();
    expect(await screen.findByText("Wakeonlan")).toBeInTheDocument();
    expect(screen.getByText(note(s2))).toBeInTheDocument();
    await showOlder();
    expect(await screen.findByText("Hibernate")).toBeInTheDocument();

    const reads = mockedPage.mock.calls.map((c) => {
      const q = new URL(String(c[0]), "http://x").searchParams;
      return `${q.get("end_time") ?? ""}:${q.get("offset") ?? ""}`;
    });
    expect(reads).toEqual([":0", `${s1}:0`, `${s1}:50`, `${s2}:0`]);
    expect(screen.getAllByText("Shutdown")).toHaveLength(1);
    expect(screen.getByText("53 entries")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Show older" })).toBeNull();
    expect(screen.queryByText(/^Complete back to/)).toBeNull();
    // Held still: an event's re-read of the cluster's Histories reads neither
    // the page nor the stretches below it again.
    await queryClient.invalidateQueries({ queryKey: ["history", "c1"] });
    expect(mockedPage).toHaveBeenCalledTimes(4);
  });

  // A kind read past its cap names its ids by the hundred: an expanded row
  // shows them collapsed, apart from its details.
  it("shows a long list of ids collapsed", async () => {
    const user = setupUser();
    mockedPage.mockReset();
    const ids = Array.from({ length: 12 }, (_, i) => `vm-${String(i)}`);
    mockedPage.mockResolvedValue({
      items: [
        makeHistoryEntry({
          kind: "observed",
          source: "detected",
          action: "changed",
          resource_type: "cluster",
          fields: ["pool"],
          details: JSON.stringify({
            notice: "changed_past_cap",
            while_over_cap: true,
            added: 12,
            added_ids: ids,
          }),
        }),
      ],
      total: 1,
    });
    const kind = historyKind(
      "probe",
      { categories: { other: "Other" } },
      { icons: {} },
    );
    renderWithProviders(<HistoryTimeline kind={kind} target={probeTarget} />);
    await user.click(
      await screen.findByText(
        "Pool past the cap: 12 added, 0 removed, 0 changed",
      ),
    );
    const list = await screen.findByText("added ids: 12 items");
    expect(list.closest("details")).not.toHaveAttribute("open");
    expect(screen.getByText(ids.join(", ")).closest("details")).toBe(
      list.closest("details"),
    );
  });

  // A notice is the observer's word on how it reads the cluster, filed when it
  // was written: expanded, it reads as that, not as a change between readings.
  it("reads a notice as the observer's own, not as a change", async () => {
    const user = setupUser();
    mockedPage.mockReset();
    mockedPage.mockResolvedValue({
      items: [
        makeHistoryEntry({
          kind: "observed",
          source: "detected",
          action: "changed",
          resource_type: "cluster",
          fields: ["token_privileges"],
          last_seen_at: "2026-03-02T10:10:00Z",
          details: JSON.stringify({ notice: "token_privileges" }),
        }),
      ],
      total: 1,
    });
    const kind = historyKind(
      "probe",
      { categories: { other: "Other" } },
      { icons: {} },
    );
    renderWithProviders(<HistoryTimeline kind={kind} target={probeTarget} />);
    await user.click(
      await screen.findByText(/The cluster token's privileges on \/ changed/),
    );
    expect(
      await screen.findByText(/Nexara's observer filed this/),
    ).toBeInTheDocument();
    expect(screen.getByText("Nexara's observer")).toBeInTheDocument();
    expect(screen.queryByText("Last seen as it was")).not.toBeInTheDocument();
    expect(
      screen.queryByText(/Nexara saw this change in Proxmox/),
    ).not.toBeInTheDocument();
  });

  // While a page turn's read is in flight the previous page stands in for
  // the last one; Show older would hold that page's entries as the last's.
  it("offers Show older only once the last page itself has come", async () => {
    const user = setupUser();
    mockedPage.mockReset();
    const page = (id: string, action: string): HistoryPage => ({
      items: [makeHistoryEntry({ id, action, resource_type: "node" })],
      total: 51,
      searched_from: "2026-01-02T03:04:05.000001Z",
    });
    let land: (p: HistoryPage) => void = () => undefined;
    mockedPage.mockImplementation((path) =>
      new URL(String(path), "http://x").searchParams.get("offset") === "50"
        ? new Promise<HistoryPage>((resolve) => {
            land = resolve;
          })
        : Promise.resolve(page("audit:2", "reboot")),
    );
    const kind = historyKind(
      "probe",
      { categories: { other: "Other" } },
      { icons: {} },
    );
    renderWithProviders(<HistoryTimeline kind={kind} target={probeTarget} />);
    expect(await screen.findByText("Reboot")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Next page" }));
    await waitFor(() => {
      expect(mockedPage).toHaveBeenCalledTimes(2);
    });
    expect(screen.getByText("Page 2 of 2")).toBeInTheDocument();
    expect(screen.getByText("Reboot")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Show older" })).toBeNull();
    land(page("audit:1", "shutdown"));
    expect(
      await screen.findByRole("button", { name: "Show older" }),
    ).toBeInTheDocument();
    expect(screen.getByText("Shutdown")).toBeInTheDocument();
  });

  // Its route reads a kind's default window when none is named, so All time
  // asks for all of it; an empty window says so, not that a filter matched
  // nothing.
  it("asks for all time when the reader picks it", async () => {
    const user = setupUser();
    mockedPage.mockReset();
    mockedPage.mockResolvedValue({ items: [], total: 0 });
    const kind = historyKind(
      "probe",
      { categories: { other: "Other" } },
      { icons: {}, defaultWindow: "7d" },
    );
    renderWithProviders(<HistoryTimeline kind={kind} target={probeTarget} />);
    expect(
      await screen.findByText(/^Nothing was recorded in this time/),
    ).toBeInTheDocument();
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
