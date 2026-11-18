import { afterEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, screen, waitFor } from "@testing-library/react";

import { apiFetch } from "@/lib/api-client";
import { useBrandingStore } from "@/stores/branding-store";
import { stubObjectUrls } from "@/test/object-url";
import { renderWithProviders } from "@/test/test-utils";
import { BrandingLogo } from "./BrandingLogo";

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock({}, { apiFetch: vi.fn() }),
);

const mockedFetch = vi.mocked(apiFetch);

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
  vi.unstubAllGlobals();
  useBrandingStore.setState({ logoUrl: null });
});

describe("BrandingLogo", () => {
  // The file is served only with the session's token, which an <img> cannot
  // send: pointing src at its API path showed every custom logo broken.
  it("shows the uploaded logo from the file read with the session", async () => {
    const { blobs } = stubObjectUrls();
    mockedFetch.mockResolvedValue(new Response("png"));
    useBrandingStore.setState({
      logoUrl: "/api/v1/settings/branding/logo-file",
    });
    renderWithProviders(
      <BrandingLogo className="logo" alt="Logo" fallback="default" />,
    );
    const img = await screen.findByAltText("Logo");
    expect(img.getAttribute("src")).toBe("blob:test/1");
    expect(mockedFetch.mock.calls[0]?.[0]).toBe(
      "/api/v1/settings/branding/logo-file",
    );
    expect(await blobs[0]?.text()).toBe("png");
  });

  // A removed logo's file stayed cached, so one uploaded after it showed the
  // old picture until the new file arrived.
  it("shows no removed logo while the next one loads", async () => {
    stubObjectUrls();
    const logo = "/api/v1/settings/branding/logo-file";
    mockedFetch.mockResolvedValueOnce(new Response("old"));
    useBrandingStore.setState({ logoUrl: logo });
    renderWithProviders(
      <BrandingLogo className="logo" alt="Logo" fallback="default" />,
    );
    await screen.findByAltText("Logo");

    act(() => {
      useBrandingStore.setState({ logoUrl: null });
    });
    mockedFetch.mockReturnValueOnce(new Promise<Response>(() => {}));
    act(() => {
      useBrandingStore.setState({ logoUrl: logo });
    });

    await waitFor(() => {
      expect(mockedFetch).toHaveBeenCalledTimes(2);
    });
    expect(screen.queryByAltText("Logo")).toBeNull();
    expect(screen.getByText("default")).toBeTruthy();
  });

  it("shows the fallback, and reads nothing, when no logo is uploaded", () => {
    renderWithProviders(<BrandingLogo className="logo" fallback="default" />);
    expect(screen.getByText("default")).toBeTruthy();
    expect(mockedFetch).not.toHaveBeenCalled();
  });
});
