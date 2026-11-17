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

// No addListener/removeListener: they are deprecated. sonner calls addListener
// only if addEventListener throws; @xterm/xterm calls it directly, and is mocked
// in tests. A test that renders the real terminal must add both methods to its
// own matchMedia mock.
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
