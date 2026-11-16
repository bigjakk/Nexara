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
(
  globalThis as unknown as { ResizeObserver: typeof ResizeObserverStub }
).ResizeObserver = ResizeObserverStub;

// No addListener/removeListener: they are deprecated, and the only library
// that calls one (sonner) does so only when addEventListener throws.
const mediaQueryList = (
  query: string,
): Omit<MediaQueryList, "addListener" | "removeListener"> => ({
  matches: false,
  media: query,
  onchange: null,
  addEventListener: () => undefined,
  removeEventListener: () => undefined,
  dispatchEvent: () => false,
});
window.matchMedia = (query: string) => mediaQueryList(query) as MediaQueryList;

Element.prototype.scrollIntoView = () => undefined;
Element.prototype.hasPointerCapture = () => false;
Element.prototype.releasePointerCapture = () => undefined;
