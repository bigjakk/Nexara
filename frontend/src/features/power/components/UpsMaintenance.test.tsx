import { beforeEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { setupUser } from "@/test/user";
import { apiClient } from "@/lib/api-client";
import { useAuthStore } from "@/stores/auth-store";
import { upsDevice } from "../power.fixtures";
import { UpsMaintenance } from "./UpsMaintenance";

vi.mock("@/lib/api-client", async () => (await import("@/test/mocks")).apiClientMock());
vi.mock("sonner", async () => (await import("@/test/mocks")).everyKindOfToastMock());

// A UPS's windows: listed with where each stands, opened from local times sent
// as instants, removed by id — offered with the global window permission.
describe("UpsMaintenance", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    useAuthStore.setState({ globalPermissions: ["view:maintenance_window", "manage:maintenance_window"] });
    const now = Date.now();
    vi.mocked(apiClient.list).mockResolvedValue([
      {
        id: "w1",
        ups_id: "u1",
        description: "Battery swap",
        starts_at: new Date(now - 60_000).toISOString(),
        ends_at: new Date(now + 3_600_000).toISOString(),
        created_by: "x",
        created_at: "",
        updated_at: "",
      },
    ]);
    vi.mocked(apiClient.post).mockResolvedValue({});
    vi.mocked(apiClient.delete).mockResolvedValue(undefined);
  });

  it("lists, opens and removes a UPS's windows", async () => {
    const user = setupUser();
    renderWithProviders(<UpsMaintenance device={upsDevice({ name: "ups01" })} />);
    expect(await screen.findByText(/Open now/)).toBeInTheDocument();
    expect(screen.getByText("Battery swap")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Add window" }));
    await user.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Add window" }));
    const [path, body] = vi.mocked(apiClient.post).mock.calls[0] ?? [];
    expect(path).toBe("/api/v1/ups-devices/u1/maintenance-windows");
    // Local times, sent as instants.
    expect(body).toEqual({ starts_at: expect.stringMatching(/Z$/) as unknown, ends_at: expect.stringMatching(/Z$/) as unknown });
    await waitFor(() => {
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    });

    await user.click(screen.getByRole("button", { name: /^Remove the window from/ }));
    expect(apiClient.delete).toHaveBeenCalledWith("/api/v1/ups-devices/u1/maintenance-windows/w1");
  });
});
