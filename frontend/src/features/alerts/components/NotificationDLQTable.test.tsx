import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/test-utils";
import { listOf, stubApi } from "@/test/fetch-stub";
import { useAuthStore } from "@/stores/auth-store";
import type { NotificationDLQEntry } from "@/types/api";
import { NotificationDLQTable } from "./NotificationDLQTable";

function entry(id: string, channelName: string): NotificationDLQEntry {
  return {
    id,
    channel_type: "webhook",
    channel_name: channelName,
    payload: {},
    last_error: "connection refused",
    attempt_count: 3,
    state: "pending",
    failure_kind: "send_failed",
    created_at: new Date(Date.now() - 2 * 3600 * 1000).toISOString(),
    updated_at: new Date().toISOString(),
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
    permissions: ["manage:notification_dlq"],
    isAuthenticated: true,
    isInitialized: true,
  });
  api = stubApi({
    "/api/v1/notification-dlq": listOf([
      entry("dlq-1", "ops-webhook"),
      entry("dlq-2", "oncall-webhook"),
    ]),
    "/api/v1/notification-dlq/summary": {
      pending: 2,
      rate_limited: 0,
      retrying: 0,
      resolved: 0,
      dismissed: 0,
    },
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
    await screen.findByRole("button", {
      name: "Delete failed notification to oncall-webhook from 2h ago",
    }),
  );
}

describe("NotificationDLQTable — delete", () => {
  it("asks first, naming the entry, and sends nothing yet", async () => {
    const user = userEvent.setup();
    renderWithProviders(<NotificationDLQTable />);
    await clickDelete(user);

    const dialog = screen.getByRole("alertdialog");
    expect(dialog).toHaveTextContent(
      "Delete the failed notification to oncall-webhook from 2h ago?",
    );
    expect(dialog).toHaveTextContent("can no longer be retried");
    expect(api.writes()).toEqual([]);
  });

  it("Cancel sends nothing", async () => {
    const user = userEvent.setup();
    renderWithProviders(<NotificationDLQTable />);
    await clickDelete(user);

    await user.click(screen.getByRole("button", { name: "Cancel" }));

    await waitFor(() => {
      expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    });
    expect(api.writes()).toEqual([]);
  });

  it("confirming deletes exactly the entry clicked, once", async () => {
    const user = userEvent.setup();
    renderWithProviders(<NotificationDLQTable />);
    await clickDelete(user);

    await user.click(
      within(screen.getByRole("alertdialog")).getByRole("button", {
        name: "Delete",
      }),
    );

    await waitFor(() => {
      expect(api.writes()).toEqual(["DELETE /api/v1/notification-dlq/dlq-2"]);
    });
    // Once: a second request arriving after the first would land here.
    await new Promise((r) => setTimeout(r, 100));
    expect(api.writes()).toEqual(["DELETE /api/v1/notification-dlq/dlq-2"]);
  });
});
