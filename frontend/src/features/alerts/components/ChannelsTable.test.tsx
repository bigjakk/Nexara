import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/test-utils";
import { listOf, stubApi } from "@/test/fetch-stub";
import { useAuthStore } from "@/stores/auth-store";
import type { NotificationChannel } from "@/types/api";
import { ChannelsTable } from "./ChannelsTable";

function channel(id: string, name: string): NotificationChannel {
  return {
    id,
    name,
    channel_type: "webhook",
    enabled: true,
    created_by: "user-1",
    created_at: "2026-08-15T00:00:00Z",
    updated_at: "2026-08-15T00:00:00Z",
  };
}

let api: ReturnType<typeof stubApi>;

beforeEach(() => {
  useAuthStore.setState({
    user: {
      id: "u1",
      email: "u@example.com",
      display_name: "Test User",
      role: "user",
    },
    permissions: ["manage:notification_channel"],
    isAuthenticated: true,
    isInitialized: true,
  });
  api = stubApi({
    "/api/v1/notification-channels": listOf([
      channel("ch-1", "ops-webhook"),
      channel("ch-2", "oncall-webhook"),
    ]),
  });
});

afterEach(() => {
  vi.unstubAllGlobals();
  useAuthStore.setState({
    user: null,
    permissions: [],
    isAuthenticated: false,
  });
});

async function clickDelete(user: ReturnType<typeof userEvent.setup>) {
  await user.click(
    await screen.findByRole("button", { name: "Delete oncall-webhook" }),
  );
}

describe("ChannelsTable — delete", () => {
  it("asks first, naming the channel and what uses it, and sends nothing yet", async () => {
    const user = userEvent.setup();
    renderWithProviders(<ChannelsTable />);
    await clickDelete(user);

    const dialog = screen.getByRole("alertdialog");
    expect(dialog).toHaveTextContent(
      "Delete notification channel oncall-webhook?",
    );
    expect(dialog).toHaveTextContent(
      "Alert rules whose escalation chain uses this channel keep that step",
    );
    expect(api.writes()).toEqual([]);
  });

  it("Cancel sends nothing", async () => {
    const user = userEvent.setup();
    renderWithProviders(<ChannelsTable />);
    await clickDelete(user);

    await user.click(screen.getByRole("button", { name: "Cancel" }));

    await waitFor(() => {
      expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    });
    expect(api.writes()).toEqual([]);
  });

  it("confirming deletes exactly the channel clicked, once", async () => {
    const user = userEvent.setup();
    renderWithProviders(<ChannelsTable />);
    await clickDelete(user);

    await user.click(
      within(screen.getByRole("alertdialog")).getByRole("button", {
        name: "Delete",
      }),
    );

    await waitFor(() => {
      expect(api.writes()).toEqual([
        "DELETE /api/v1/notification-channels/ch-2",
      ]);
    });
    // Once: a second request arriving after the first would land here.
    await new Promise((r) => setTimeout(r, 100));
    expect(api.writes()).toEqual(["DELETE /api/v1/notification-channels/ch-2"]);
  });
});
