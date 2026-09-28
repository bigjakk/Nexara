import { afterEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/test-utils";
import { listOf } from "@/test/fetch-stub";
import type { APIKeyResponse } from "@/types/api";
import { APIKeysPage } from "./APIKeysPage";

function apiKey(id: string, name: string): APIKeyResponse {
  return {
    id,
    name,
    key_prefix: "nxra_0000",
    expires_at: null,
    last_used_at: null,
    last_used_ip: null,
    is_revoked: false,
    created_at: "2026-01-01T00:00:00Z",
  };
}

// The API as the page uses it: the list, a create that adds a key to it, and
// a revoke that marks a key revoked (its Revoke button then goes).
function stubKeys(initial: APIKeyResponse[]) {
  let keys = initial;
  const json = (body: unknown, status = 200) =>
    Promise.resolve(
      new Response(JSON.stringify(body), {
        status,
        headers: { "Content-Type": "application/json" },
      }),
    );
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url =
        typeof input === "string"
          ? input
          : input instanceof URL
            ? input.href
            : input.url;
      const method = init?.method ?? "GET";
      if (url === "/api/v1/auth/refresh") {
        return Promise.resolve(new Response("{}", { status: 401 }));
      }
      if (url === "/api/v1/api-keys" && method === "GET") {
        return json(listOf(keys));
      }
      if (url === "/api/v1/api-keys" && method === "POST") {
        const created = apiKey(`key${String(keys.length + 1)}`, "deploy02");
        keys = [...keys, created];
        return json({ ...created, key: "nxra_0000example" }, 201);
      }
      if (method === "DELETE" && url.startsWith("/api/v1/api-keys/")) {
        const id = url.slice("/api/v1/api-keys/".length);
        keys = keys.map((k) => (k.id === id ? { ...k, is_revoked: true } : k));
        return Promise.resolve(new Response(null, { status: 204 }));
      }
      return json({ error: `unstubbed ${method} ${url}` }, 404);
    }),
  );
}

afterEach(() => {
  vi.unstubAllGlobals();
});

type User = ReturnType<typeof userEvent.setup>;

async function createKey(user: User, from: string) {
  await user.click(await screen.findByRole("button", { name: from }));
  const form = await screen.findByRole("dialog");
  await user.type(within(form).getByLabelText("Name"), "deploy02");
  await user.click(within(form).getByRole("button", { name: "Create Key" }));
  const created = await screen.findByRole("dialog", {
    name: "API Key Created",
  });
  await user.click(within(created).getByRole("button", { name: "Done" }));
}

describe("APIKeysPage — focus after its dialogs", () => {
  it("puts focus on the keys card once a revoked key's Revoke button has gone", async () => {
    stubKeys([apiKey("key1", "deploy01")]);
    const user = userEvent.setup();
    renderWithProviders(<APIKeysPage />);
    await user.click(await screen.findByRole("button", { name: /Revoke/ }));
    const confirm = await screen.findByRole("alertdialog");

    await user.click(
      within(confirm).getByRole("button", { name: "Revoke Key" }),
    );

    await waitFor(() => {
      expect(
        screen.queryByRole("button", { name: /Revoke/ }),
      ).not.toBeInTheDocument();
    });
    await waitFor(() => {
      expect(document.activeElement).toBe(
        screen.getByRole("region", { name: "Your API keys" }),
      );
    });
  });

  // The key is shown in a dialog of its own, opened as the form closes: its
  // Done goes back to what opened the form.
  it("returns focus to Create API Key from the key the form created", async () => {
    stubKeys([apiKey("key1", "deploy01")]);
    const user = userEvent.setup();
    renderWithProviders(<APIKeysPage />);

    await createKey(user, "Create API Key");

    await waitFor(() => {
      expect(document.activeElement).toBe(
        screen.getByRole("button", { name: "Create API Key" }),
      );
    });
  });

  it("puts focus on the keys card when the form was opened from the empty list, which the first key takes away", async () => {
    stubKeys([]);
    const user = userEvent.setup();
    renderWithProviders(<APIKeysPage />);

    await createKey(user, "Create your first API key");

    await waitFor(() => {
      expect(document.activeElement).toBe(
        screen.getByRole("region", { name: "Your API keys" }),
      );
    });
    expect(
      screen.queryByRole("button", { name: "Create your first API key" }),
    ).not.toBeInTheDocument();
  });
});
