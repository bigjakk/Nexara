import { afterEach, describe, expect, it, vi } from "vitest";
import { renderHook, waitFor } from "@testing-library/react";

import { ApiClientError, apiClient } from "@/lib/api-client";
import { createTestQueryClient, createWrapper } from "@/test/test-utils";
import { useCephStatus } from "./ceph-queries";

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

const mockedGet = vi.mocked(apiClient.get);

afterEach(() => {
  vi.clearAllMocks();
});

describe("useCephStatus", () => {
  // A cluster without Ceph answers that through a 502; the page asked twice
  // on every visit and again every minute, each a failed request in the console.
  it.each([
    {
      after: "the cluster's answer that it has no Ceph",
      message: "binary not installed: /usr/bin/ceph-mon",
      calls: 1,
    },
    { after: "any other failure", message: "connection refused", calls: 2 },
  ])("after $after, sends $calls request(s)", async ({ message, calls }) => {
    mockedGet.mockRejectedValue(
      new ApiClientError(502, { error: "bad_gateway", message }),
    );
    // The hook's own retry rule overrides the test client's; no wait between.
    const client = createTestQueryClient();
    client.setDefaultOptions({
      queries: { ...client.getDefaultOptions().queries, retryDelay: 0 },
    });
    const { result } = renderHook(() => useCephStatus("c1"), {
      wrapper: createWrapper({ client }),
    });
    await waitFor(() => {
      expect(result.current.isError).toBe(true);
    });
    expect(mockedGet).toHaveBeenCalledTimes(calls);
  });
});
