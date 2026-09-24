import { expect } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { render, type RenderOptions } from "@testing-library/react";
import type { ReactElement, ReactNode } from "react";

function createTestQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: {
        retry: false,
        gcTime: 0,
      },
    },
  });
}

interface WrapperProps {
  children: ReactNode;
}

export function createWrapper() {
  const queryClient = createTestQueryClient();
  function Wrapper({ children }: WrapperProps) {
    return (
      <QueryClientProvider client={queryClient}>
        <MemoryRouter>{children}</MemoryRouter>
      </QueryClientProvider>
    );
  }
  return Wrapper;
}

export function renderWithProviders(
  ui: ReactElement,
  options?: Omit<RenderOptions, "wrapper">,
) {
  return render(ui, { wrapper: createWrapper(), ...options });
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
