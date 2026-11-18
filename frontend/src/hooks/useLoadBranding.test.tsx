import { afterEach, describe, expect, it, vi } from "vitest";
import { act, renderHook, waitFor } from "@testing-library/react";

import { apiClient, apiFetch } from "@/lib/api-client";
import { DEFAULT_FAVICON, useBrandingStore } from "@/stores/branding-store";
import { stubObjectUrls } from "@/test/object-url";
import { createWrapper } from "@/test/test-utils";
import { useLoadBranding } from "./useLoadBranding";

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock({}, { apiFetch: vi.fn() }),
);

function iconHref() {
  return document
    .querySelector<HTMLLinkElement>('link[rel="icon"]')
    ?.getAttribute("href");
}

afterEach(() => {
  vi.clearAllMocks();
  vi.unstubAllGlobals();
  document.querySelector('link[rel="icon"]')?.remove();
  useBrandingStore.setState({ faviconUrl: null, loaded: false });
});

describe("useLoadBranding", () => {
  // The tab's icon pointed at the file's API path, which the browser loads
  // without the session's token: an uploaded favicon showed no icon at all.
  it("puts the uploaded favicon in the tab once it has loaded, and the default back without it", async () => {
    stubObjectUrls();
    vi.mocked(apiClient.get).mockResolvedValue({
      "branding.favicon_url": "/api/v1/settings/branding/favicon-file",
    });
    vi.mocked(apiFetch).mockResolvedValue(new Response("ico"));
    renderHook(() => useLoadBranding(), { wrapper: createWrapper() });

    await waitFor(() => {
      expect(iconHref()).toBe("blob:test/1");
    });
    act(() => {
      useBrandingStore.getState().setFaviconUrl(null);
    });
    await waitFor(() => {
      expect(iconHref()).toBe(DEFAULT_FAVICON);
    });
  });
});
