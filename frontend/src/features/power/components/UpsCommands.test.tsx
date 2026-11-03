import { beforeEach, describe, expect, it, vi } from "vitest";
import { screen, within } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { setupUser } from "@/test/user";
import { apiClient } from "@/lib/api-client";
import { useAuthStore } from "@/stores/auth-store";
import { nutServer, upsCommandRun, upsDevice, upsFeed } from "../power.fixtures";
import { UpsCommands } from "./UpsCommands";

vi.mock("@/lib/api-client", async () => (await import("@/test/mocks")).apiClientMock());
vi.mock("sonner", async () => (await import("@/test/mocks")).everyKindOfToastMock());

describe("UpsCommands", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    // manage:node on some cluster: the server checks the ones the UPS powers.
    useAuthStore.setState({ globalPermissions: ["view:ups", "execute:ups"], permissions: ["view:ups", "execute:ups", "manage:node"] });
    vi.mocked(apiClient.list).mockImplementation((path: unknown) => {
      const p = String(path);
      if (p.includes("nut-servers")) return Promise.resolve([nutServer({ has_password: true, username: "nexara" })]);
      if (p.includes("/clusters/c1/vms")) {
        return Promise.resolve([
          { id: "v1", node_id: "n1", status: "running", template: false },
          { id: "v2", node_id: "n1", status: "stopped", template: false },
        ]);
      }
      return Promise.resolve([]);
    });
    vi.mocked(apiClient.post).mockResolvedValue(upsCommandRun());
  });

  // An everyday command runs at once; a test asks first; one that cuts the
  // power shows what it powers and takes the UPS's name typed, which goes to
  // the server too.
  it("runs a command by the confirmation its tier asks for", async () => {
    const user = setupUser();
    renderWithProviders(
      <UpsCommands
        device={upsDevice({
          name: "ups01",
          feeds: [upsFeed()],
          commands_listed_at: "2026-11-03T12:00:00Z",
          commands: [
            { name: "beeper.mute", description: "Mute the beeper", tier: "everyday", confirm: "" },
            { name: "test.battery.start.quick", description: "Start a quick battery test", tier: "test", confirm: "plain" },
            { name: "load.off", description: "Turn off the load immediately", tier: "cuts_power", confirm: "typed" },
          ],
        })}
      />,
    );

    // Run waits for the server's account to be known.
    expect(await screen.findByText(/the first command is its test/)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Run beeper.mute" }));
    expect(apiClient.post).toHaveBeenCalledWith("/api/v1/ups-devices/u1/commands", { command: "beeper.mute" });

    await user.click(screen.getByRole("button", { name: "Run test.battery.start.quick" }));
    expect(apiClient.post).toHaveBeenCalledTimes(1);
    await user.click(within(await screen.findByRole("alertdialog")).getByRole("button", { name: "Run" }));
    expect(apiClient.post).toHaveBeenLastCalledWith("/api/v1/ups-devices/u1/commands", { command: "test.battery.start.quick" });

    await user.click(screen.getByRole("button", { name: "Run load.off" }));
    const dialog = await screen.findByRole("alertdialog");
    expect(await within(dialog).findByText(/1 guest running/)).toBeInTheDocument();
    const cut = within(dialog).getByRole("button", { name: "Cut the power" });
    expect(cut).toBeDisabled();
    await user.type(within(dialog).getByLabelText(/to confirm/), "ups01");
    await user.click(cut);
    expect(apiClient.post).toHaveBeenLastCalledWith("/api/v1/ups-devices/u1/commands", { command: "load.off", confirm: "ups01" });
  });
});
