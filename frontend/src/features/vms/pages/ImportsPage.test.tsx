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
  // The button opens the import dialog, gated as the Create menu gates it: an
  // import creates a VM, so manage:vm_import alone would be refused.
  it.each([
    ["offers Import VM to a user who may import and create VMs", true],
    ["does not offer it to one who may only manage imports", false],
  ])("%s", (_name, granted) => {
    grantPermissions("manage:vm_import", ...(granted ? ["manage:vm"] : []));
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
