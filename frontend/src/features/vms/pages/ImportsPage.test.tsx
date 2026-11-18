import { describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { grantPermissions } from "@/test/permissions";
import { ImportsPage } from "./ImportsPage";

vi.mock("@/features/dashboard/api/dashboard-queries", () => ({
  useClusters: () => ({ data: [{ id: "c1", name: "cluster01" }] }),
}));
vi.mock("../api/import-queries", () => ({
  useImportJobs: () => ({ data: [], isLoading: false }),
  useImportSources: () => ({ data: [] }),
  useCancelImport: () => ({ mutate: vi.fn(), isPending: false }),
  useDeleteImportSource: () => ({
    mutate: vi.fn(),
    isPending: false,
    isError: false,
  }),
  useRegisterEsxiSource: () => ({
    mutate: vi.fn(),
    isPending: false,
    isError: false,
  }),
}));

describe("ImportsPage", () => {
  // The button opens the import dialog the Create menu gates on the same
  // permission; without it the dialog's own requests would be refused.
  it.each([
    ["offers Import VM to a user who may manage imports", true],
    ["does not offer it to one who may not", false],
  ])("%s", (_name, granted) => {
    grantPermissions(...(granted ? ["manage:vm_import"] : []));
    renderWithProviders(<ImportsPage />);
    // The page has settled on its cluster: the panels are drawn.
    expect(
      screen.getByRole("heading", { name: "Import sources" }),
    ).toBeInTheDocument();

    const button = screen.queryByRole("button", { name: "Import VM" });
    if (granted) expect(button).toBeInTheDocument();
    else expect(button).not.toBeInTheDocument();
  });
});
