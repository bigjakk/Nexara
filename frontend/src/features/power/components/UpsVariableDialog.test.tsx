import { describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { setupUser } from "@/test/user";
import { apiClient } from "@/lib/api-client";
import { upsCommandRun, upsDevice } from "../power.fixtures";
import type { UpsCommandRun } from "../types/ups";
import { UpsVariableDialog } from "./UpsVariableDialog";

vi.mock("@/lib/api-client", async () => (await import("@/test/mocks")).apiClientMock());

describe("UpsVariableDialog", () => {
  // A variable that can change the power waits for the confirmation, then the
  // dialog follows the run to the driver's answer.
  it("sets a power setting once confirmed and shows how it went", async () => {
    const sent = upsCommandRun({ id: "r9", kind: "set", name: "battery.charge.low", argument: "20" });
    let runs: UpsCommandRun[] = [];
    vi.mocked(apiClient.list).mockImplementation(() => Promise.resolve(runs));
    vi.mocked(apiClient.put).mockImplementation(() => {
      runs = [{ ...sent, status: "succeeded" }];
      return Promise.resolve(sent);
    });
    const user = setupUser();
    renderWithProviders(
      <UpsVariableDialog
        device={upsDevice({ name: "ups01" })}
        variable={{ name: "battery.charge.low", value: "10", type: "string", max_len: 10, editable: true, affects_power: true, confirm: "plain" }}
        onClose={() => undefined}
      />,
    );

    const field = screen.getByLabelText("New value");
    await user.clear(field);
    await user.type(field, "20");
    const set = screen.getByRole("button", { name: "Set" });
    expect(set).toBeDisabled();
    await user.click(screen.getByRole("checkbox"));
    await user.click(set);

    expect(apiClient.put).toHaveBeenCalledWith("/api/v1/ups-devices/u1/vars/battery.charge.low", { value: "20" });
    expect(await screen.findByRole("status")).toHaveTextContent("Set.");
  });
});
