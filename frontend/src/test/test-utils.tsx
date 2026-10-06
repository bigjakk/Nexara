import { expect } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, type MemoryRouterProps } from "react-router-dom";
import { render, type RenderOptions } from "@testing-library/react";
import type { ReactElement, ReactNode } from "react";

/**
 * The client most tests run on: no retries, and a query dropped from the cache
 * as soon as nothing watches it. `keepCache` leaves TanStack's own cache times,
 * for a test whose queries must outlive their component (a remount, or the
 * next user on the same client).
 */
export function createTestQueryClient({
  keepCache = false,
}: { keepCache?: boolean } = {}): QueryClient {
  return new QueryClient({
    defaultOptions: keepCache
      ? { queries: { retry: false }, mutations: { retry: false } }
      : { queries: { retry: false, gcTime: 0 } },
  });
}

interface WrapperProps {
  children: ReactNode;
}

export interface ProviderOptions {
  /** The client to run on; a createTestQueryClient() one unless given. */
  client?: QueryClient | undefined;
  /** MemoryRouter props (initialEntries, ...), or false for no router at all. */
  router?: MemoryRouterProps | false | undefined;
}

export function createWrapper({
  client = createTestQueryClient(),
  router = {},
}: ProviderOptions = {}) {
  function Wrapper({ children }: WrapperProps) {
    return (
      <QueryClientProvider client={client}>
        {router ? (
          <MemoryRouter {...router}>{children}</MemoryRouter>
        ) : (
          children
        )}
      </QueryClientProvider>
    );
  }
  return Wrapper;
}

/**
 * Renders `ui` in createWrapper's providers. The client it ran on comes back as
 * `queryClient`, to read what the test's queries and mutations left in it.
 */
export function renderWithProviders(
  ui: ReactElement,
  {
    client = createTestQueryClient(),
    router,
    ...options
  }: Omit<RenderOptions, "wrapper"> & ProviderOptions = {},
) {
  const view = render(ui, {
    wrapper: createWrapper({ client, router }),
    ...options,
  });
  return { ...view, queryClient: client };
}

/**
 * Asserts that `el` is on screen as text, as far as a test can tell.
 * toBeVisible reads computed style (display, visibility, opacity), the
 * hidden attribute and a closed <details>, but Tailwind's stylesheet is not
 * loaded in tests, so what a class does is invisible to it. This also
 * refuses `sr-only` — the class that keeps text for screen readers and off
 * the screen — on `el` or any ancestor. Any other class that hides it goes
 * unseen.
 */
export function expectOnScreen(el: HTMLElement): void {
  expect(el).toBeVisible();
  expect(el.closest(".sr-only")).toBeNull();
}
