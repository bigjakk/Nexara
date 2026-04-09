/**
 * Tests for the recent-server-URLs MRU list in `lib/secure-storage.ts`.
 *
 * The list is a JSON-encoded string array stored under a SecureStore key.
 * The wrapper has dedup-on-add and a max-size cap. We mock SecureStore
 * with a simple in-memory map to make the wrapper testable in isolation.
 *
 * Other secure-storage methods are thin pass-throughs to expo-secure-store
 * and aren't worth dedicated tests — the only non-trivial logic in this
 * file is the recents MRU and the JSON parse safety on getPermissions /
 * getRecentServerUrls. Both are covered here.
 */

import { beforeEach, describe, expect, it, jest } from "@jest/globals";

const mockMemoryStore = new Map<string, string>();

jest.mock("expo-secure-store", () => ({
  getItemAsync: jest.fn((key: string) =>
    Promise.resolve(mockMemoryStore.get(key) ?? null),
  ),
  setItemAsync: jest.fn((key: string, value: string) => {
    mockMemoryStore.set(key, value);
    return Promise.resolve();
  }),
  deleteItemAsync: jest.fn((key: string) => {
    mockMemoryStore.delete(key);
    return Promise.resolve();
  }),
}));

import { secureStorage } from "./secure-storage";

beforeEach(() => {
  mockMemoryStore.clear();
});

describe("secureStorage.addRecentServerUrl", () => {
  it("starts with an empty list", async () => {
    expect(await secureStorage.getRecentServerUrls()).toEqual([]);
  });

  it("adds a new URL to the head of the list", async () => {
    await secureStorage.addRecentServerUrl("https://a.example.com");
    await secureStorage.addRecentServerUrl("https://b.example.com");
    const list = await secureStorage.getRecentServerUrls();
    expect(list).toEqual(["https://b.example.com", "https://a.example.com"]);
  });

  it("dedupes — adding an existing URL moves it to the head", async () => {
    await secureStorage.addRecentServerUrl("https://a.example.com");
    await secureStorage.addRecentServerUrl("https://b.example.com");
    await secureStorage.addRecentServerUrl("https://c.example.com");
    // Re-adding 'a' should move it to the head, not duplicate
    await secureStorage.addRecentServerUrl("https://a.example.com");

    const list = await secureStorage.getRecentServerUrls();
    expect(list).toEqual([
      "https://a.example.com",
      "https://c.example.com",
      "https://b.example.com",
    ]);
    // No duplicate of 'a'
    expect(list.filter((u) => u === "https://a.example.com")).toHaveLength(1);
  });

  it("caps the list at MAX_RECENT_SERVERS=5 entries", async () => {
    for (let i = 1; i <= 7; i++) {
      await secureStorage.addRecentServerUrl(`https://server-${String(i)}.example.com`);
    }
    const list = await secureStorage.getRecentServerUrls();
    expect(list).toHaveLength(5);
    // The 5 most recent (3-7) survive; 1 and 2 fell off
    expect(list[0]).toBe("https://server-7.example.com");
    expect(list[4]).toBe("https://server-3.example.com");
    expect(list).not.toContain("https://server-1.example.com");
    expect(list).not.toContain("https://server-2.example.com");
  });
});

describe("secureStorage.removeRecentServerUrl", () => {
  it("removes a specific URL from the list", async () => {
    await secureStorage.addRecentServerUrl("https://a.example.com");
    await secureStorage.addRecentServerUrl("https://b.example.com");
    await secureStorage.addRecentServerUrl("https://c.example.com");

    await secureStorage.removeRecentServerUrl("https://b.example.com");

    const list = await secureStorage.getRecentServerUrls();
    expect(list).toEqual(["https://c.example.com", "https://a.example.com"]);
  });

  it("is a no-op for URLs not in the list", async () => {
    await secureStorage.addRecentServerUrl("https://a.example.com");
    await secureStorage.removeRecentServerUrl("https://nonexistent.example.com");
    const list = await secureStorage.getRecentServerUrls();
    expect(list).toEqual(["https://a.example.com"]);
  });
});

describe("secureStorage.getRecentServerUrls — JSON parse safety", () => {
  it("returns empty list when storage is empty", async () => {
    expect(await secureStorage.getRecentServerUrls()).toEqual([]);
  });

  it("returns empty list when storage value is malformed JSON", async () => {
    mockMemoryStore.set("nx.recent_servers", "not valid json {{{");
    expect(await secureStorage.getRecentServerUrls()).toEqual([]);
  });

  it("returns empty list when storage value is JSON but not an array", async () => {
    mockMemoryStore.set("nx.recent_servers", JSON.stringify({ foo: "bar" }));
    expect(await secureStorage.getRecentServerUrls()).toEqual([]);
  });

  it("returns empty list when array contains non-string entries", async () => {
    mockMemoryStore.set(
      "nx.recent_servers",
      JSON.stringify(["https://valid.com", 123, null, true]),
    );
    // The validator rejects the WHOLE array if any entry isn't a string —
    // safer than partial acceptance
    expect(await secureStorage.getRecentServerUrls()).toEqual([]);
  });
});

describe("secureStorage.getPermissions — JSON parse safety", () => {
  it("returns empty list when storage is empty", async () => {
    expect(await secureStorage.getPermissions()).toEqual([]);
  });

  it("returns empty list when storage value is malformed JSON", async () => {
    mockMemoryStore.set("nx.permissions", "{{{ corrupt");
    expect(await secureStorage.getPermissions()).toEqual([]);
  });

  it("returns empty list when array contains non-string entries", async () => {
    mockMemoryStore.set(
      "nx.permissions",
      JSON.stringify(["execute:vm", 42, "view:cluster"]),
    );
    // Defensive — drop the whole list rather than silently passing through
    // a corrupted permission set
    expect(await secureStorage.getPermissions()).toEqual([]);
  });

  it("round-trips a valid permission list", async () => {
    const perms = ["execute:vm", "view:cluster", "manage:alert"];
    await secureStorage.setPermissions(perms);
    expect(await secureStorage.getPermissions()).toEqual(perms);
  });
});

describe("secureStorage.isBiometricEnrolled", () => {
  it("defaults to false", async () => {
    expect(await secureStorage.isBiometricEnrolled()).toBe(false);
  });

  it("setBiometricEnrolled(true) persists, false deletes", async () => {
    await secureStorage.setBiometricEnrolled(true);
    expect(await secureStorage.isBiometricEnrolled()).toBe(true);

    await secureStorage.setBiometricEnrolled(false);
    expect(await secureStorage.isBiometricEnrolled()).toBe(false);
    // Delete path actually removes the key, doesn't just write "0"
    expect(mockMemoryStore.has("nx.biometric_enrolled")).toBe(false);
  });
});

describe("secureStorage.hasBiometricPromptBeenShown", () => {
  it("defaults to false", async () => {
    expect(await secureStorage.hasBiometricPromptBeenShown()).toBe(false);
  });

  it("round-trips true/false", async () => {
    await secureStorage.setBiometricPromptShown(true);
    expect(await secureStorage.hasBiometricPromptBeenShown()).toBe(true);

    await secureStorage.setBiometricPromptShown(false);
    expect(await secureStorage.hasBiometricPromptBeenShown()).toBe(false);
    expect(mockMemoryStore.has("nx.biometric_prompt_shown")).toBe(false);
  });

  it("survives clearAll so users aren't re-pestered after sign-out", async () => {
    await secureStorage.setBiometricPromptShown(true);
    await secureStorage.clearAll();
    // clearAll wipes tokens/permissions/biometric-enrolled but must
    // preserve the "already asked" flag — otherwise every sign-out would
    // trigger the prompt again on next login, defeating the "Not now"
    // choice.
    expect(await secureStorage.hasBiometricPromptBeenShown()).toBe(true);
  });
});
