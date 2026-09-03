import { describe, it, expect, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/test-utils";
import { PBSTaskTable } from "./PBSTaskTable";
import type { PBSTask } from "../types/backup";

vi.mock("../api/backup-queries", () => ({
  usePBSTaskLog: () => ({ data: [], isLoading: false }),
}));

/** PAGE_SIZE is 25, so 60 tasks is three pages. */
function tasks(count: number): PBSTask[] {
  return Array.from({ length: count }, (_, i) => ({
    upid: `UPID:pbs:0000${String(i).padStart(4, "0")}::backup::`,
    node: "pbs",
    pid: 1000 + i,
    starttime: 1_780_000_000 + i,
    endtime: 1_780_000_060 + i,
    status: "OK",
    worker_type: i % 2 === 0 ? "backup" : "verify",
    user: "root@pam",
  }));
}

const pager = () => screen.getByText(/^Page \d+ of \d+$/).textContent;
const next = () => screen.getByRole("button", { name: "Next page" });

describe("PBSTaskTable paging", () => {
  // The pager is two bare chevrons, so its buttons carry their whole
  // accessible name in an aria-label. Without them a screen reader announces
  // two unlabelled buttons with nothing to tell them apart.
  it("gives both pager buttons an accessible name", () => {
    renderWithProviders(<PBSTaskTable tasks={tasks(60)} pbsId="pbs-1" />);

    expect(
      screen.getByRole("button", { name: "Previous page" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Next page" }),
    ).toBeInTheDocument();
  });

  it("pages through a long task list", async () => {
    const user = userEvent.setup();
    renderWithProviders(<PBSTaskTable tasks={tasks(60)} pbsId="pbs-1" />);

    expect(pager()).toBe("Page 1 of 3");
    await user.click(next());
    expect(pager()).toBe("Page 2 of 3");
  });

  // The reason the reset exists. Page 2 of the unfiltered list is not page 2 of
  // whatever survives a filter, so staying put would drop the operator at an
  // arbitrary offset into results they have not seen the start of.
  it("returns to the first page when a filter changes", async () => {
    const user = userEvent.setup();
    renderWithProviders(<PBSTaskTable tasks={tasks(60)} pbsId="pbs-1" />);

    await user.click(next());
    expect(pager()).toBe("Page 2 of 3");

    // 30 of the 60 are worker_type "backup" — still two pages, so the page
    // number survives clamping and only the reset can move it.
    await user.selectOptions(screen.getByDisplayValue("All Types"), "backup");
    expect(pager()).toBe("Page 1 of 2");
  });

  // Each filter is a separate comparison in the reset, so each needs its own
  // case: with only the type filter and the search box covered, deleting the
  // statusFilter comparison passed the entire suite.
  it("returns to the first page when the status filter changes", async () => {
    const user = userEvent.setup();
    renderWithProviders(<PBSTaskTable tasks={tasks(60)} pbsId="pbs-1" />);

    await user.click(next());
    expect(pager()).toBe("Page 2 of 3");

    // All 60 are finished with status OK, so "ok" keeps every row and the page
    // number stays in range — only the reset can move it.
    await user.selectOptions(screen.getByDisplayValue("All Statuses"), "ok");
    expect(pager()).toBe("Page 1 of 3");
  });

  it("returns to the first page when the search box changes", async () => {
    const user = userEvent.setup();
    renderWithProviders(<PBSTaskTable tasks={tasks(60)} pbsId="pbs-1" />);

    await user.click(next());
    expect(pager()).toBe("Page 2 of 3");

    await user.type(screen.getByPlaceholderText("Search tasks..."), "root");
    expect(pager()).toBe("Page 1 of 3");
  });

  // The trigger is the filter values, not the filtered array. `tasks` is
  // repolled every few seconds, and keying the reset off the derived list would
  // yank the operator back to page 1 mid-read on every poll.
  it("stays on the page when the task list is repolled", async () => {
    const user = userEvent.setup();
    const { rerender } = renderWithProviders(
      <PBSTaskTable tasks={tasks(60)} pbsId="pbs-1" />,
    );

    await user.click(next());
    expect(pager()).toBe("Page 2 of 3");

    // A fresh array with the same contents, as a refetch produces.
    rerender(<PBSTaskTable tasks={tasks(60)} pbsId="pbs-1" />);
    expect(pager()).toBe("Page 2 of 3");

    // And a poll that genuinely adds a task must not move them either.
    rerender(<PBSTaskTable tasks={tasks(61)} pbsId="pbs-1" />);
    expect(pager()).toBe("Page 2 of 3");
  });

  // Shrinking the list is the other way the page can fall out of range, and it
  // is handled by clamping rather than by the reset — the operator keeps their
  // position when the list grows back.
  //
  // Note the page has to end up genuinely OUT of range for this to test
  // anything: shrinking from three pages to two while sitting on page 2 leaves
  // the number valid, and passes with no clamp at all.
  it("clamps to the last page when the list shrinks under it", async () => {
    const user = userEvent.setup();
    const { rerender } = renderWithProviders(
      <PBSTaskTable tasks={tasks(60)} pbsId="pbs-1" />,
    );

    await user.click(next());
    await user.click(next());
    expect(pager()).toBe("Page 3 of 3");

    // Two pages now, so page 3 no longer exists.
    rerender(<PBSTaskTable tasks={tasks(26)} pbsId="pbs-1" />);
    expect(pager()).toBe("Page 2 of 2");
    // The clamped page shows its row rather than slicing past the end. Counting
    // rows would NOT show this: an out-of-range slice renders one empty-state
    // row, so the count is 1 either way.
    expect(screen.queryByText("No matching tasks.")).not.toBeInTheDocument();

    // Clamping displays a different page without writing `page` back, so the
    // operator returns to where they were when the list grows again. That is
    // deliberate; without this case a "tidy-up" adding setPage(safePage) would
    // silently drop it and leave every test green.
    rerender(<PBSTaskTable tasks={tasks(60)} pbsId="pbs-1" />);
    expect(pager()).toBe("Page 3 of 3");
  });
});
