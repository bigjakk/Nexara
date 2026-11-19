import { beforeEach, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { grantPermissions } from "@/test/permissions";
import { apiClient } from "@/lib/api-client";
import { ImportVMDialog } from "./ImportVMDialog";

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

beforeEach(() => {
  vi.mocked(apiClient.list).mockResolvedValue([]);
});

// An import is offered on manage:vm_import and manage:vm, but a URL download
// into a store takes manage:storage even for an OVA, and so does adding the
// import content type the empty source list asks for. An upload, and
// registering an ESXi source, take manage:vm_import.
it.each<[string, string[], boolean]>([
  ["manage:vm_import", ["manage:vm_import"], false],
  ["manage:storage too", ["manage:vm_import", "manage:storage"], true],
])("for a role holding %s", async (_, grants, storage) => {
  grantPermissions(...grants);
  renderWithProviders(
    <ImportVMDialog open onOpenChange={vi.fn()} clusterId="c1" />,
  );

  const advice = await screen.findByText(/^No import-capable storage found\./);
  expect(advice).toHaveTextContent(/register an ESXi source/i);
  expect(screen.queryByText(/content type to a directory/) !== null).toBe(
    storage,
  );
  expect(
    screen.queryByRole("button", { name: "Download OVA from URL" }) !== null,
  ).toBe(storage);
  expect(
    screen.getByRole("button", { name: "Upload OVA" }),
  ).toBeInTheDocument();
});
