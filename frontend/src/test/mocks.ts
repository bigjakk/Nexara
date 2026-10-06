import { vi } from "vitest";

/**
 * The two modules most component tests replace. vi.mock is hoisted above the
 * file's imports, so its factory reaches these through a dynamic import; the
 * test still reads the stubs with the real names (`vi.mocked(apiClient.post)`):
 *
 *   vi.mock("@/lib/api-client", async () =>
 *     (await import("@/test/mocks")).apiClientMock(),
 *   );
 *   vi.mock("sonner", async () => (await import("@/test/mocks")).sonnerMock());
 */

type Verb = "get" | "list" | "post" | "put" | "patch" | "delete";

/**
 * `@/lib/api-client` with only its transport replaced: the real module is
 * spread in, because api-error.ts imports ApiClientError from it (a mock that
 * supplies only apiClient makes describeError throw on every render) and the
 * session (clearTokens, storeTokens, sessionScope) stays the real one.
 * apiClient gets get, list, post, put and delete as bare vi.fn()s; `verbs`
 * replaces one of them, or adds patch, e.g. to hand a request to a mock the
 * file holds.
 */
export async function apiClientMock(
  verbs: Partial<Record<Verb, (...args: never[]) => unknown>> = {},
) {
  const actual =
    await vi.importActual<typeof import("@/lib/api-client")>(
      "@/lib/api-client",
    );
  return {
    ...actual,
    apiClient: {
      get: vi.fn(),
      list: vi.fn(),
      post: vi.fn(),
      put: vi.fn(),
      delete: vi.fn(),
      ...verbs,
    },
  };
}

/** sonner, with the toasts a component raises (success, warning, error) as spies. */
export function sonnerMock() {
  return { toast: { success: vi.fn(), warning: vi.fn(), error: vi.fn() } };
}

/**
 * sonner with a spy for every kind of toast, callable as `toast(...)` too: for
 * a test that says exactly what a run raised, of any kind (toastsRaised in
 * late-toast-sessions.ts), so that "toasts nothing" is not satisfied by a
 * success or a warning.
 */
export function everyKindOfToastMock() {
  return {
    toast: Object.assign(vi.fn(), {
      success: vi.fn(),
      info: vi.fn(),
      warning: vi.fn(),
      error: vi.fn(),
      message: vi.fn(),
      loading: vi.fn(),
    }),
  };
}
