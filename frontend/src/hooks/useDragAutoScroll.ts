import { useEffect } from "react";

interface Options {
  /** CSS selector for the scroll container that should auto-scroll during drag. */
  selector: string;
  /** Distance (px) from the top/bottom edge at which auto-scroll starts. */
  edgeSize?: number;
  /** Maximum scroll speed in pixels per animation frame. */
  maxSpeed?: number;
}

/**
 * Auto-scrolls a container while an HTML5 drag is in progress and the pointer
 * is near its top or bottom edge. Replaces the browser's built-in
 * drag-autoscroll, which has a tiny (~10 px) hot zone and a fixed, fast scroll
 * speed — frustrating when dropping into a target that's just out of view.
 *
 * Scroll speed ramps linearly from 0 at the edge of the hot zone up to
 * `maxSpeed` at the container's edge, so the user can feather the speed by
 * moving the pointer closer to or further from the edge.
 */
export function useDragAutoScroll({
  selector,
  edgeSize = 60,
  maxSpeed = 14,
}: Options): void {
  useEffect(() => {
    let rafId: number | null = null;
    let scrollDelta = 0;
    let container: HTMLElement | null = null;

    function tick() {
      rafId = null;
      if (container && scrollDelta !== 0) {
        container.scrollTop += scrollDelta;
        rafId = requestAnimationFrame(tick);
      }
    }

    function onDragOver(e: DragEvent) {
      container ??= document.querySelector<HTMLElement>(selector);
      if (!container) return;

      const rect = container.getBoundingClientRect();
      const y = e.clientY;

      if (y < rect.top || y > rect.bottom) {
        scrollDelta = 0;
        return;
      }

      const distFromTop = y - rect.top;
      const distFromBottom = rect.bottom - y;

      if (distFromTop < edgeSize) {
        scrollDelta = -Math.ceil(maxSpeed * (1 - distFromTop / edgeSize));
      } else if (distFromBottom < edgeSize) {
        scrollDelta = Math.ceil(maxSpeed * (1 - distFromBottom / edgeSize));
      } else {
        scrollDelta = 0;
      }

      if (scrollDelta !== 0 && rafId === null) {
        rafId = requestAnimationFrame(tick);
      }
    }

    function stop() {
      scrollDelta = 0;
      if (rafId !== null) {
        cancelAnimationFrame(rafId);
        rafId = null;
      }
      container = null;
    }

    document.addEventListener("dragover", onDragOver);
    document.addEventListener("dragend", stop);
    document.addEventListener("drop", stop);

    return () => {
      document.removeEventListener("dragover", onDragOver);
      document.removeEventListener("dragend", stop);
      document.removeEventListener("drop", stop);
      stop();
    };
  }, [selector, edgeSize, maxSpeed]);
}
