import { describe, it, expect, vi } from "vitest";
import { act, renderHook } from "@testing-library/react";
import { createTestQueryClient, createWrapper } from "@/test/test-utils";
import { apiClient } from "@/lib/api-client";
import { useUpsertSettings } from "./settings-queries";

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

describe("useUpsertSettings", () => {
  // A refresh started while another write is still out would cache that
  // setting's old value, so it waits for every write; the failure still
  // surfaces, once.
  it("refreshes only after every write has settled, then fails", async () => {
    let landSecond: (v: unknown) => void = () => undefined;
    vi.mocked(apiClient.put)
      .mockRejectedValueOnce(new Error("first failed"))
      .mockReturnValueOnce(
        new Promise((resolve) => {
          landSecond = resolve;
        }),
      );
    const client = createTestQueryClient();
    const refresh = vi.spyOn(client, "invalidateQueries");
    const { result } = renderHook(() => useUpsertSettings(), {
      wrapper: createWrapper({ client }),
    });

    let outcome: Promise<unknown> = Promise.resolve();
    act(() => {
      outcome = result.current.mutateAsync([
        { key: "a", value: 1 },
        { key: "b", value: 2 },
      ]);
    });
    const failure = outcome.catch((e: unknown) => e);
    await act(async () => {
      await Promise.resolve();
    });
    expect(refresh).not.toHaveBeenCalled();

    await act(async () => {
      landSecond({});
      await failure;
    });
    expect(((await failure) as Error).message).toBe("first failed");
    expect(refresh).toHaveBeenCalledTimes(1);
  });
});
