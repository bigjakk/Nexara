/** Shared bits between the Events-page TasksPanel and folder/VM-scoped task
 * views. Lives outside the component files so fast refresh keeps working. */

/** Rows per page. Shared because TaskHistoryTable derives the page count from
 *  it while each view uses it to build its own query offset — two copies that
 *  disagreed would page the table off the end of the results. */
export const PAGE_SIZE = 50;

export const selectClass =
  "flex h-9 w-[200px] rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-xs transition-colors focus-visible:outline-hidden focus-visible:ring-1 focus-visible:ring-ring";

export const statusFilters = [
  { value: "", label: "All Statuses" },
  { value: "running", label: "Running" },
  { value: "completed", label: "Completed" },
  { value: "failed", label: "Failed" },
  { value: "stopped", label: "Stopped" },
] as const;
