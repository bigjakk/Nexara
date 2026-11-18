import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import { apiClient } from "@/lib/api-client";
import { useBrandingStore } from "@/stores/branding-store";
import { renderWithProviders } from "@/test/test-utils";
import { BrandingPage } from "./BrandingPage";

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock({}, { apiFetch: vi.fn() }),
);
vi.mock("sonner", async () => (await import("@/test/mocks")).sonnerMock());

const mockedGet = vi.mocked(apiClient.get);
const mockedDelete = vi.mocked(apiClient.delete);

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
  useBrandingStore.setState({
    appTitle: "Nexara",
    logoUrl: null,
    faviconUrl: null,
    loaded: false,
  });
});

describe("BrandingPage", () => {
  // The server sends a saved title decoded; the page parsed it a second time
  // and fell into its error boundary on every load after a title was saved.
  it("shows a saved title as the server sends it", async () => {
    mockedGet.mockResolvedValue({ "branding.app_title": "Acme Platform" });
    renderWithProviders(<BrandingPage />);
    expect(await screen.findByDisplayValue("Acme Platform")).toBeTruthy();
  });

  it("puts the default logo back by removing its setting", async () => {
    const logo = "/api/v1/settings/branding/logo-file";
    useBrandingStore.setState({ logoUrl: logo });
    mockedGet.mockResolvedValue({ "branding.logo_url": logo });
    mockedDelete.mockResolvedValue(undefined);
    renderWithProviders(<BrandingPage />);
    await userEvent.click(screen.getByRole("button", { name: "Use default" }));
    await vi.waitFor(() => {
      expect(mockedDelete).toHaveBeenCalledWith(
        "/api/v1/settings/branding.logo_url?scope=global",
      );
    });
    await vi.waitFor(() => {
      expect(useBrandingStore.getState().logoUrl).toBeNull();
    });
  });
});
