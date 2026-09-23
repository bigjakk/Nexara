import { describe, expect, expectTypeOf, it, vi, beforeEach } from "vitest";
import { renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createElement, type ReactNode } from "react";

import {
  useSyslogConfig,
  useSaveSyslogConfig,
  type SyslogConfig,
  type SyslogSaveWarning,
} from "./events-queries";
import { usePermissions } from "@/hooks/usePermissions";

const getMock = vi.fn();

vi.mock("@/lib/api-client", () => ({
  apiClient: {
    get: (path: string) => getMock(path) as unknown,
  },
}));
vi.mock("@/hooks/usePermissions", () => ({
  usePermissions: vi.fn(),
}));

const stored: SyslogConfig = {
  enabled: true,
  host: "syslog.example.com",
  port: 514,
  protocol: "udp",
  facility: 16,
  tls_skip_verify: false,
};

/**
 * A user holding exactly `held`. The answers are worked out from `held`, so a
 * hook that asked about the wrong permission would get the wrong answer rather
 * than a blanket yes.
 */
function grant(held: string[]) {
  const has = (action: string, resource: string) =>
    held.includes(`${action}:${resource}`);
  vi.mocked(usePermissions).mockReturnValue({
    hasPermission: has,
    canView: (resource: string) => has("view", resource),
    canManage: (resource: string) => has("manage", resource),
  } as unknown as ReturnType<typeof usePermissions>);
}

function wrapper() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return ({ children }: { children: ReactNode }) =>
    createElement(QueryClientProvider, { client }, children);
}

describe("useSyslogConfig", () => {
  beforeEach(() => {
    getMock.mockReset();
    getMock.mockResolvedValue(stored);
  });

  it("reads the config for a user who can manage the audit log", async () => {
    grant(["view:audit", "manage:audit"]);
    const { result } = renderHook(() => useSyslogConfig(), {
      wrapper: wrapper(),
    });

    await waitFor(() => {
      expect(result.current.isSuccess).toBe(true);
    });
    expect(getMock).toHaveBeenCalledTimes(1);
    expect(getMock).toHaveBeenCalledWith("/api/v1/audit-log/syslog-config");
    expect(result.current.data).toEqual(stored);
  });

  it("sends nothing for a Viewer, whose read could only 403", async () => {
    // view:audit is every Viewer's; GET /audit-log/syslog-config needs a
    // global manage:audit (registry_audit.go).
    grant(["view:audit"]);
    const { result } = renderHook(() => useSyslogConfig(), {
      wrapper: wrapper(),
    });

    // Long enough for the fetch the other test waits for to have started.
    await new Promise((resolve) => setTimeout(resolve, 50));
    expect(getMock).not.toHaveBeenCalled();
    expect(result.current.fetchStatus).toBe("idle");
    expect(result.current.data).toBeUndefined();
  });
});

describe("useSaveSyslogConfig", () => {
  it("types its reply as the stored config or the forwarder's warning", () => {
    // Enforced by tsc -b, which type-checks this file; at run time this is a
    // no-op. Typed as the config alone, the warning reply could be written into
    // the ["syslog-config"] cache without complaint, and the card would then
    // crash reading the protocol it does not have.
    expectTypeOf<
      ReturnType<typeof useSaveSyslogConfig>["data"]
    >().toEqualTypeOf<SyslogConfig | SyslogSaveWarning | undefined>();
  });
});
