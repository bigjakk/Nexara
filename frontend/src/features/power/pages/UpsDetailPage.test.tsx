import { describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Route, Routes } from "react-router-dom";
import { renderWithProviders } from "@/test/test-utils";
import { apiClient } from "@/lib/api-client";
import { upsDevice } from "../power.fixtures";
import { UpsDetailPage } from "./UpsDetailPage";

vi.mock("@/lib/api-client", async () => (await import("@/test/mocks")).apiClientMock());

describe("UpsDetailPage", () => {
  it("lists upsd's variables with their descriptions and filters them", async () => {
    vi.mocked(apiClient.get).mockImplementation((path: unknown) =>
      Promise.resolve(
        String(path).includes("/metrics")
          ? { range: "1h", bucket_seconds: 10, points: [] }
          : upsDevice({
              description: "Rack UPS",
              vars: { "battery.charge": "100", "input.voltage": "230.4", "ups.status": "OL" },
              var_descriptions: { "input.voltage": "Input voltage (V)" },
            }),
      ),
    );
    vi.mocked(apiClient.list).mockResolvedValue([]);
    const user = userEvent.setup();
    renderWithProviders(
      <Routes>
        <Route path="/power/:upsId" element={<UpsDetailPage />} />
      </Routes>,
      { router: { initialEntries: ["/power/u1"] } },
    );

    await user.click(await screen.findByRole("tab", { name: "Variables (3)" }));
    expect(screen.getByText("battery.charge")).toBeInTheDocument();
    await user.type(screen.getByLabelText("Filter variables"), "input");
    expect(screen.queryByText("battery.charge")).not.toBeInTheDocument();
    expect(screen.getByText("Input voltage (V)")).toBeInTheDocument();
  });
});
