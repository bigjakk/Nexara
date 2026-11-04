import { describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import { Route, Routes } from "react-router-dom";
import { renderWithProviders } from "@/test/test-utils";
import { setupUser } from "@/test/user";
import { apiClient } from "@/lib/api-client";
import { useAuthStore } from "@/stores/auth-store";
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
});
