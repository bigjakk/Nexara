import { describe, expect, it } from "vitest";
import { sessionLabel } from "@/lib/session-label";
import type { UserSession } from "@/types/api";

function session(overrides: Partial<UserSession> = {}): UserSession {
  return {
    id: "00000000-0000-0000-0000-000000000001",
    device_name: "",
    device_type: "web",
    user_agent: "",
    ip_address: "192.0.2.10",
    created_at: "2026-09-08T12:00:00Z",
    last_used_at: "2026-09-08T12:30:00Z",
    expires_at: "2026-09-15T12:00:00Z",
    is_current: false,
    ...overrides,
  };
}

describe("sessionLabel", () => {
  it("prefers an explicitly supplied device name", () => {
    expect(
      sessionLabel(
        session({
          device_name: "Ops laptop",
          user_agent: "Mozilla/5.0 (X11; Linux x86_64) Firefox/130.0",
        }),
      ),
    ).toBe("Ops laptop");
  });

  it("falls back when there is nothing to go on", () => {
    expect(sessionLabel(session())).toBe("Unknown device");
  });

  // The precedence cases below are the whole reason this function is tested.
  // Every major UA lies about being another browser, so a naive ordered set of
  // checks silently labels most sessions "Safari" and makes two different
  // devices indistinguishable in the list — which defeats the feature.
  it.each([
    {
      name: "Edge is not reported as Chrome",
      ua: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36 Edg/128.0.0.0",
      want: "Edge on Windows",
    },
    {
      name: "Opera is not reported as Chrome",
      ua: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36 OPR/114.0.0.0",
      want: "Opera on Windows",
    },
    {
      name: "Chrome is not reported as Safari",
      ua: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36",
      want: "Chrome on macOS",
    },
    {
      name: "real Safari is still Safari",
      ua: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.6 Safari/605.1.15",
      want: "Safari on macOS",
    },
    {
      name: "Firefox on Linux",
      ua: "Mozilla/5.0 (X11; Linux x86_64; rv:130.0) Gecko/20100101 Firefox/130.0",
      want: "Firefox on Linux",
    },
    {
      name: "Android reports Android, not Linux",
      ua: "Mozilla/5.0 (Linux; Android 14) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Mobile Safari/537.36",
      want: "Chrome on Android",
    },
    {
      name: "iPhone reports iOS",
      ua: "Mozilla/5.0 (iPhone; CPU iPhone OS 17_6 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.6 Mobile/15E148 Safari/604.1",
      want: "Safari on iOS",
    },
  ])("$name", ({ ua, want }) => {
    expect(sessionLabel(session({ user_agent: ua }))).toBe(want);
  });

  it("degrades to a bare browser name when the platform is unrecognised", () => {
    expect(
      sessionLabel(
        session({ user_agent: "Mozilla/5.0 (Haiku) Firefox/130.0" }),
      ),
    ).toBe("Firefox");
  });

  // Real dev-stack data: three concurrent curl sessions all rendered as
  // "Browser" before this, which is both wrong and useless in a list whose
  // purpose is telling apart what you do not recognise.
  it.each([
    { name: "curl", ua: "curl/8.5.0", want: "curl" },
    {
      name: "python-requests",
      ua: "python-requests/2.32.3",
      want: "python-requests",
    },
    { name: "a Go client", ua: "Go-http-client/2.0", want: "Go-http-client" },
  ])(
    "names a non-browser client after its product token ($name)",
    ({ ua, want }) => {
      expect(sessionLabel(session({ user_agent: ua }))).toBe(want);
    },
  );

  it("does not report an unrecognised browser as 'Mozilla'", () => {
    // Every browser UA opens "Mozilla/5.0", so that token names nothing.
    expect(
      sessionLabel(
        session({ user_agent: "Mozilla/5.0 (compatible; Unknown/1.0)" }),
      ),
    ).toBe("Unknown client");
  });
});
