import { describe, it, expect, vi, beforeEach } from "vitest";
import { waitFor } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { apiClient, ApiClientError } from "@/lib/api-client";
import { usePreferencesStore } from "@/stores/preferences-store";
import { useThemeStore } from "@/stores/theme-store";
import { settingsKeys } from "@/features/settings/api/settings-queries";
import { LoadSavedPreferences } from "./LoadSavedPreferences";

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

const fail = (status: number) =>
  new ApiClientError(status, { error: "e", message: "" });

// The previous user of this browser left green and decimal units behind.
beforeEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
  document.documentElement.style.removeProperty("--primary");
  usePreferencesStore.getState().loadFromJSON({
    accentColor: "green",
    byteUnit: "decimal",
  });
});

describe("LoadSavedPreferences", () => {
  // Protects that a new browser gets the saved preferences without a visit
  // to Appearance, that a user with none saved does not inherit the last
  // user's, and that a failed load leaves the browser's copy alone.
  it.each([
    {
      answer: () =>
        Promise.resolve({
          value: { accentColor: "purple", dateFormat: "iso" },
        }),
      want: { accentColor: "purple", byteUnit: "binary", dateFormat: "iso" },
      accent: true,
    },
    {
      answer: () => Promise.reject(fail(404)),
      want: { accentColor: "default", byteUnit: "binary" },
      accent: false,
    },
    {
      answer: () => Promise.reject(fail(500)),
      want: { accentColor: "green", byteUnit: "decimal" },
      accent: false,
    },
  ])("applies the server's answer: $want.accentColor", async (c) => {
    vi.mocked(apiClient.get).mockImplementation(c.answer);
    const { queryClient } = renderWithProviders(<LoadSavedPreferences />);

    await waitFor(() => {
      const key = settingsKeys.detail("user.preferences", "user");
      expect(queryClient.getQueryState(key)?.status).not.toBe("pending");
    });
    expect(apiClient.get).toHaveBeenCalledTimes(1);
    expect(usePreferencesStore.getState().preferences).toMatchObject(c.want);
    const primary =
      document.documentElement.style.getPropertyValue("--primary");
    expect(primary !== "").toBe(c.accent);
  });

  // Protects the theme saved per user: the saved one is applied, and a change
  // made after that is saved with the rest (not before: the local copy may be
  // the previous user's).
  it("applies the saved theme and saves a change to it", async () => {
    useThemeStore.getState().setMode("light");
    vi.mocked(apiClient.get).mockResolvedValue({
      value: { accentColor: "purple", theme: "dark" },
    });
    vi.mocked(apiClient.put).mockResolvedValue({});
    renderWithProviders(<LoadSavedPreferences />);

    await waitFor(() => {
      expect(useThemeStore.getState().mode).toBe("dark");
    });
    expect(document.documentElement.classList.contains("dark")).toBe(true);
    expect(apiClient.put).not.toHaveBeenCalled();

    useThemeStore.getState().setMode("light");
    await waitFor(() => {
      expect(apiClient.put).toHaveBeenCalledWith(
        "/api/v1/settings/user.preferences",
        {
          value: expect.objectContaining({
            theme: "light",
            accentColor: "purple",
          }) as unknown,
          scope: "user",
        },
      );
    });
  });
});
