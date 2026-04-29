import "@testing-library/jest-dom/vitest";
import "@/lib/i18n"; // Initialize i18next for tests

// Radix UI components rely on ResizeObserver / matchMedia / scrollIntoView /
// pointer-capture APIs that jsdom doesn't ship with. Polyfill the bare
// minimum so Select / Dialog open without throwing in tests. Setup only
// runs under vitest's jsdom env, so unconditional assignment is fine.
class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
(globalThis as unknown as { ResizeObserver: typeof ResizeObserverStub }).ResizeObserver = ResizeObserverStub;

window.matchMedia = (query: string) => ({
  matches: false,
  media: query,
  onchange: null,
  addEventListener: () => undefined,
  removeEventListener: () => undefined,
  addListener: () => undefined,
  removeListener: () => undefined,
  dispatchEvent: () => false,
});

Element.prototype.scrollIntoView = () => undefined;
Element.prototype.hasPointerCapture = () => false;
Element.prototype.releasePointerCapture = () => undefined;
