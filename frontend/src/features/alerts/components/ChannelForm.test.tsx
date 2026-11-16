import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/test-utils";
import { bodiesOf, stubApi } from "@/test/fetch-stub";
import { parseWebhookHeaders } from "../lib/webhook-headers";
import { ChannelForm } from "./ChannelForm";

let api: ReturnType<typeof stubApi>;

beforeEach(() => {
  api = stubApi({});
});

afterEach(() => {
  vi.unstubAllGlobals();
});

async function openWebhookForm() {
  const user = userEvent.setup();
  renderWithProviders(<ChannelForm />);
  await user.click(screen.getByRole("button", { name: "Add channel" }));
  await user.type(screen.getByLabelText("Name"), "hook01");
  await user.click(screen.getByRole("combobox", { name: "Channel type" }));
  await user.click(await screen.findByRole("option", { name: "Generic webhook" }));
  await user.type(screen.getByLabelText("URL"), "https://hooks.example.com/x");
  return user;
}

describe("parseWebhookHeaders", () => {
  it.each([
    ["", {}, null],
    ['{"Authorization": "Bearer x"}', { Authorization: "Bearer x" }, null],
    ['{"Authorization": "Bearer x"', null, "json"],
    ['["a"]', null, "shape"],
    ['{"X-Count": 1}', null, "shape"],
  ])("%s", (raw, headers, error) => {
    expect(parseWebhookHeaders(raw)).toEqual({ headers, error });
  });
});

describe("ChannelForm — webhook", () => {
  // Invalid headers used to be sent as {}, dropping the Authorization typed.
  it("holds Create on headers that are not JSON, and sends them once fixed", async () => {
    const user = await openWebhookForm();
    const headers = screen.getByLabelText("Headers (JSON, optional)");
    await user.click(headers);
    await user.paste('{"Authorization": "Bearer x"');
    const create = within(screen.getByRole("dialog")).getByRole("button", { name: "Create" });
    expect(screen.getByRole("alert")).toHaveTextContent("Not valid JSON");
    expect(create).toBeDisabled();

    await user.paste("}");
    expect(create).toBeEnabled();
    await user.click(create);
    await waitFor(() => {
      expect(api.writes()).toEqual(["POST /api/v1/notification-channels"]);
    });
    const [body] = bodiesOf("POST", "/api/v1/notification-channels") as {
      config: { method: string; headers: unknown };
    }[];
    expect(body?.config.method).toBe("POST");
    expect(body?.config.headers).toEqual({ Authorization: "Bearer x" });
  });

  // Every send refuses GET (webhook.go).
  it("does not offer GET", async () => {
    const user = await openWebhookForm();
    await user.click(screen.getByRole("combobox", { name: "Method" }));
    expect(await screen.findByRole("option", { name: "POST" })).toBeInTheDocument();
    expect(screen.queryByRole("option", { name: "GET" })).not.toBeInTheDocument();
  });
});

describe("ChannelForm — private address", () => {
  // The server refuses a target on a private network until it is confirmed;
  // confirming resends the same channel with the allowance.
  it("asks to confirm a private target and resends with allow_private_address", async () => {
    const json = (status: number, body: unknown) =>
      new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
    let posts = 0;
    stubApi({}, {
      "POST /api/v1/notification-channels": () =>
        ++posts === 1
          ? json(422, {
              error: "private_address_confirm_required",
              message: "private",
              details: { ip: "10.0.0.5", address_kind: "private" },
            })
          : json(201, { id: "ch-1" }),
    });
    const user = await openWebhookForm();
    await user.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Create" }));
    await user.click(await screen.findByRole("button", { name: "Continue with private address" }));

    await waitFor(() => {
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    });
    const bodies = bodiesOf("POST", "/api/v1/notification-channels") as {
      allow_private_address?: boolean;
    }[];
    expect(bodies.map((b) => b.allow_private_address)).toEqual([undefined, true]);
  });

  // A confirmation is for the address that was refused: a changed target is
  // asked about again, not sent with the allowance.
  it("drops the prompt when the target changes", async () => {
    const json = (status: number, body: unknown) =>
      new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
    stubApi({}, {
      "POST /api/v1/notification-channels": () =>
        json(422, {
          error: "private_address_confirm_required",
          message: "private",
          details: { ip: "10.0.0.5", address_kind: "private" },
        }),
    });
    const user = await openWebhookForm();
    await user.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Create" }));
    expect(await screen.findByRole("button", { name: "Continue with private address" })).toBeInTheDocument();

    await user.type(screen.getByLabelText("URL"), "2");
    expect(screen.queryByRole("button", { name: "Continue with private address" })).not.toBeInTheDocument();
    await user.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Create" }));
    await screen.findByRole("button", { name: "Continue with private address" });
    const bodies = bodiesOf("POST", "/api/v1/notification-channels") as {
      allow_private_address?: boolean;
    }[];
    expect(bodies.map((b) => b.allow_private_address)).toEqual([undefined, undefined]);
  });
});
