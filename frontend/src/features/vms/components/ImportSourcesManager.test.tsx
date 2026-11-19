import { expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { apiClient } from "@/lib/api-client";
import { renderWithProviders } from "@/test/test-utils";
import { grantPermissions } from "@/test/permissions";
import type { ImportSource } from "@/types/api";
import { ImportSourcesManager } from "./ImportSourcesManager";

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

const store01 = (content: string): ImportSource => ({
  storage: "store01",
  type: "dir",
  content,
  shared: true,
  node: "pve-01",
});

/** The cluster's import sources, and its import jobs. */
function serve(sources: ImportSource[], jobs: unknown[] = []) {
  vi.mocked(apiClient.list).mockImplementation((path: unknown) =>
    Promise.resolve(String(path).endsWith("/vm-imports") ? jobs : sources),
  );
}

// Removing deletes a store only when it holds nothing but imports; from one that
// holds other content it takes the import content type off, which takes
// manage:storage as well.
it.each([
  [
    "a store holding only imports",
    store01("import"),
    ["manage:vm_import"],
    "Delete store01",
    /removes the storage definition/,
  ],
  [
    "a store holding other content",
    store01("images,import"),
    ["manage:vm_import", "manage:storage"],
    "Remove import from store01",
    /only takes the import content type off/,
  ],
  [
    "a store holding other content, without manage:storage",
    store01("images,import"),
    ["manage:vm_import"],
    null,
    null,
  ],
])("%s", async (_name, source, permissions, button, says) => {
  grantPermissions(...permissions);
  serve([source]);
  renderWithProviders(<ImportSourcesManager clusterId="c1" />);

  expect(await screen.findByText("store01")).toBeInTheDocument();
  const remove = screen.queryByRole("button", { name: /store01$/ });
  expect(remove?.getAttribute("aria-label") ?? null).toBe(button);
  if (remove && says) {
    await userEvent.click(remove);
    expect(
      await screen.findByRole("dialog", { name: "Remove import source?" }),
    ).toHaveTextContent(says);
  }
});

// An import still reading from the source would fail with it, and a live one's
// VM stop; the server refuses it (409), and the dialog says why first.
it("does not remove a source an import in progress reads from", async () => {
  grantPermissions("manage:vm_import");
  serve(
    [store01("import")],
    [{ status: "running", source_ref: "store01:import/linux01.ova" }],
  );
  renderWithProviders(<ImportSourcesManager clusterId="c1" />);

  await userEvent.click(
    await screen.findByRole("button", { name: "Delete store01" }),
  );
  expect(
    await screen.findByRole("dialog", { name: "Remove import source?" }),
  ).toHaveTextContent(/An import in progress reads from it/);
  expect(screen.getByRole("button", { name: "Remove source" })).toBeDisabled();
});
