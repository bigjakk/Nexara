import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { apiClient } from "@/lib/api-client";
import { createConsoleTokenMinter } from "./console-queries";

vi.mock("@/lib/api-client", () => ({
  apiClient: { post: vi.fn(), get: vi.fn() },
}));

const mockedPost = vi.mocked(apiClient.post);

const SCOPE = {
  clusterId: "cluster-1",
  node: "node1",
  type: "vm_vnc" as const,
  vmid: 101,
};

describe("createConsoleTokenMinter", () => {
  beforeEach(() => {
    let n = 0;
    mockedPost.mockReset();
    mockedPost.mockImplementation(() => {
      n++;
      return Promise.resolve({ token: `token-${String(n)}`, expires_in: 60 });
    });
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-08-18T12:00:00Z"));
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("reuses a still-valid token instead of re-minting", async () => {
    const mint = createConsoleTokenMinter();

    expect(await mint(SCOPE)).toBe("token-1");

    // The whole auto-reconnect backoff (1+2+4+8+10 = 25s) must fit inside
    // one token — that is what collapses six audit rows into one.
    vi.setSystemTime(new Date("2026-08-18T12:00:25Z"));
    expect(await mint(SCOPE)).toBe("token-1");

    expect(mockedPost).toHaveBeenCalledTimes(1);
  });

  it("re-mints once the cached token nears expiry", async () => {
    const mint = createConsoleTokenMinter();
    await mint(SCOPE);

    // 56s in: past the 60s TTL minus the 5s safety margin.
    vi.setSystemTime(new Date("2026-08-18T12:00:56Z"));
    expect(await mint(SCOPE)).toBe("token-2");
    expect(mockedPost).toHaveBeenCalledTimes(2);
  });

  it("re-mints when the scope changes", async () => {
    const mint = createConsoleTokenMinter();
    await mint(SCOPE);

    // A live migration moves the guest — the cached token is locked to the
    // old node and would be rejected at upgrade.
    expect(await mint({ ...SCOPE, node: "node2" })).toBe("token-2");
    expect(mockedPost).toHaveBeenCalledTimes(2);

    // ...and the new scope is what gets cached from here on.
    expect(await mint({ ...SCOPE, node: "node2" })).toBe("token-2");
    expect(mockedPost).toHaveBeenCalledTimes(2);
  });

  it("keeps caches independent per console session", async () => {
    const mintA = createConsoleTokenMinter();
    const mintB = createConsoleTokenMinter();

    expect(await mintA(SCOPE)).toBe("token-1");
    expect(await mintB(SCOPE)).toBe("token-2");
    expect(mockedPost).toHaveBeenCalledTimes(2);
  });
});
