import { useEffect, useRef, useState } from "react";

interface AnimatedNumberProps {
  value: number;
  format: (v: number) => string;
  durationMs?: number;
}

/** Eases a displayed number toward `value` whenever it changes. */
export function AnimatedNumber({
  value,
  format,
  durationMs = 500,
}: AnimatedNumberProps) {
  const [display, setDisplay] = useState(value);
  const prevRef = useRef(value);

  useEffect(() => {
    const from = prevRef.current;
    prevRef.current = value;
    if (from === value) return;
    let raf = 0;
    const t0 = performance.now();
    const step = (now: number) => {
      const p = Math.min(1, (now - t0) / durationMs);
      const eased = 1 - Math.pow(1 - p, 3);
      setDisplay(from + (value - from) * eased);
      if (p < 1) raf = requestAnimationFrame(step);
    };
    raf = requestAnimationFrame(step);
    return () => {
      cancelAnimationFrame(raf);
    };
  }, [value, durationMs]);

  return <>{format(display)}</>;
}
