import { describe, expect, it, vi } from "vitest";
import { screen, within } from "@testing-library/react";
import { Route, Routes } from "react-router-dom";
import { renderWithProviders } from "@/test/test-utils";
import { setupUser } from "@/test/user";
import { apiClient } from "@/lib/api-client";
import { useAuthStore } from "@/stores/auth-store";
import { grantPermissions } from "@/test/permissions";
import { SearchBar } from "./SearchBar";

vi.mock("@/lib/api-client", async () => (await import("@/test/mocks")).apiClientMock());

// A holder of global view:ups finds the Power page and a UPS, named with its NUT
// server, which opens the UPS's page.
describe("SearchBar", () => {
  it("finds the Power page and a UPS", async () => {
    useAuthStore.setState({ globalPermissions: ["view:ups"], permissions: ["view:ups"] });
    vi.mocked(apiClient.list).mockImplementation((path: unknown) =>
      Promise.resolve(
        (String(path).startsWith("/api/v1/search")
          ? [{ type: "ups", id: "u1", name: "ups01", server: "nut01", cluster_id: "", cluster_name: "" }]
          : []) as never,
      ),
    );
    const user = setupUser();
    renderWithProviders(
      <Routes>
        <Route path="/" element={<SearchBar />} />
        <Route path="/power/:upsId" element={<p>The UPS page</p>} />
      </Routes>,
      { router: { initialEntries: ["/"] } },
    );

    await user.click(screen.getByRole("button", { name: /Search or jump to/ }));
    await user.type(screen.getByRole("combobox"), "ups");
    expect(await screen.findByRole("option", { name: /Power/ })).toBeInTheDocument();
    await user.click(await screen.findByRole("option", { name: /ups01.*nut01/ }));
    expect(await screen.findByText("The UPS page")).toBeInTheDocument();
  });

  // Held on one cluster only, view:ups opens nothing: every UPS route takes it
  // install-wide, so the Power page would be a page of 403s.
  it("leaves the Power page out for a cluster's view:ups", async () => {
    useAuthStore.setState({ globalPermissions: [], permissions: ["view:ups"] });
    vi.mocked(apiClient.list).mockResolvedValue([] as never);
    const user = setupUser();
    renderWithProviders(<SearchBar />);

    await user.click(screen.getByRole("button", { name: /Search or jump to/ }));
    await user.type(screen.getByRole("combobox"), "power");
    expect(await screen.findByText(/No results/i)).toBeInTheDocument();
    expect(screen.queryByRole("option", { name: /Power/ })).not.toBeInTheDocument();
  });

  // Each create takes what its route declares; the palette offered a Viewer both.
  it.each([
    [["manage:vm"], /Create virtual machine/, /Create container/],
    [["manage:container"], /Create container/, /Create virtual machine/],
  ])("with %j offers only the create it allows", async (permissions, offered, refused) => {
    grantPermissions(...permissions);
    vi.mocked(apiClient.list).mockResolvedValue([] as never);
    const user = setupUser();
    renderWithProviders(<SearchBar />);

    await user.click(screen.getByRole("button", { name: /Search or jump to/ }));
    await user.type(screen.getByRole("combobox"), "create");
    expect(await screen.findByRole("option", { name: offered })).toBeInTheDocument();
    expect(screen.queryByRole("option", { name: refused })).not.toBeInTheDocument();
  });

  // A running VM whose vCPUs are halted reads as its halted state here too,
  // not as Running (lib/guest-state.ts is the one source).
  it("shows a halted VM's dot as its halted state", async () => {
    useAuthStore.setState({ globalPermissions: ["view:cluster"], permissions: ["view:cluster"] });
    vi.mocked(apiClient.list).mockImplementation((path: unknown) =>
      Promise.resolve(
        (String(path).startsWith("/api/v1/search")
          ? [{ type: "vm", id: "v1", name: "linux01", vmid: 101, status: "running", run_state: "io-error", cluster_id: "c1", cluster_name: "cluster01" }]
          : []) as never,
      ),
    );
    const user = setupUser();
    renderWithProviders(<SearchBar />);

    await user.click(screen.getByRole("button", { name: /Search or jump to/ }));
    await user.type(screen.getByRole("combobox"), "linux");
    const row = await screen.findByRole("option", { name: /linux01/ });
    expect(within(row).getByRole("img", { name: "I/O error" })).toBeInTheDocument();
  });
});
