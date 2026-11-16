/// <reference types="vite/client" />
import { describe, expect, it } from "vitest";

// A shadcn <Button> whose only child is a Lucide icon has no accessible name (the svg is
// aria-hidden), so a screen reader announces a bare "button". Not the lowercase <button>:
// the sidebar trees' toggles want the node's name and aria-expanded instead (policed
// below). `title` counts, as the accessible-name algorithm falls back to it.

// A leading-slash glob resolves against Vite's root (frontend/), so it can
// never reach the stale copies of the tree in git worktrees under
// .claude/worktrees/. Same reasoning as dialog.guard.test.tsx.
const sources: Record<string, string> = import.meta.glob("/src/**/*.tsx", {
  eager: true,
  query: "?raw",
  import: "default",
});

/** Attributes the accessible-name algorithm will fall back to. */
const NAME_ATTRS = ["aria-label", "aria-labelledby", "title"];

/**
 * Read the whole `<Button …>` opening tag starting at `start`.
 *
 * A plain `/<Button[^>]*>/` stops at the first `>`, which is wrong the moment a
 * prop holds an arrow function (`onClick={() => …}`) or a string containing an
 * angle bracket — and it would then drop the rest of the tag, including the
 * aria-label we came to look for.
 */
function readOpeningTag(source: string, start: number): string {
  let depth = 0;
  let quote: string | null = null;
  for (let i = start; i < source.length; i++) {
    const ch = source[i];
    if (quote !== null) {
      if (ch === quote) quote = null;
      continue;
    }
    if (ch === '"' || ch === "'" || ch === "`") {
      quote = ch;
      continue;
    }
    if (ch === "{") depth++;
    else if (ch === "}") depth--;
    else if (ch === ">" && depth === 0) return source.slice(start, i + 1);
  }
  return source.slice(start);
}

interface IconButton {
  file: string;
  icon: string;
  tag: string;
}

/** Every `<Button>…</Button>` whose body is a single self-closing icon. */
function iconOnlyButtons(file: string, source: string): IconButton[] {
  const found: IconButton[] = [];
  for (const m of source.matchAll(/<Button\b/g)) {
    const tag = readOpeningTag(source, m.index);
    if (tag.endsWith("/>")) continue; // self-closing: no children at all
    const bodyStart = m.index + tag.length;
    const bodyEnd = source.indexOf("</Button>", bodyStart);
    if (bodyEnd === -1) continue;
    const body = source.slice(bodyStart, bodyEnd).trim();
    // Exactly one self-closing element and nothing else.
    const single = /^<([A-Z]\w*)\b[^>]*\/>$/.exec(body);
    if (single?.[1]) found.push({ file, icon: single[1], tag });
  }
  return found;
}

describe("icon-only Button accessible names", () => {
  const all = Object.entries(sources).flatMap(([file, source]) =>
    iconOnlyButtons(file, source),
  );

  // If this ever finds nothing, the parser has drifted and every assertion
  // below is passing on an empty set.
  it("finds the icon-only buttons it is meant to police", () => {
    expect(all.length).toBeGreaterThan(100);
    expect(all.some((b) => /^Chevrons?(?:Left|Right)$/.test(b.icon))).toBe(
      true,
    );
    expect(all.some((b) => b.icon === "Trash2")).toBe(true);
  });

  it("gives every icon-only Button an accessible name", () => {
    const unnamed = all
      .filter((b) => !NAME_ATTRS.some((a) => b.tag.includes(a)))
      .map((b) => `${b.file} <Button><${b.icon} /></Button>`);

    expect(unnamed).toEqual([]);
  });
});

// The disclosure half: the sidebar trees' lowercase <button> chevron toggles are outside the
// rule above, since a static label is the wrong shape for a control whose state changes.
// Anything that declares itself a disclosure must say what it discloses; the three tree
// files have no render tests, so nothing else catches a toggle losing its name.
describe("disclosure button accessible names", () => {
  const disclosures = Object.entries(sources).flatMap(([file, source]) =>
    [...source.matchAll(/<button\b/g)]
      .map((m) => {
        const tag = readOpeningTag(source, m.index);
        const start = m.index + tag.length;
        const end = source.indexOf("</button>", start);
        // Everything left once the nested tags are stripped. A chevron-only
        // button leaves nothing; one wrapping a `<span>{label}</span>` leaves
        // `{label}`, which renders its own name and needs no attribute.
        const body =
          end === -1 ? "" : source.slice(start, end).replace(/<[^>]*>/g, "");
        return { file, tag, hasText: body.trim() !== "" };
      })
      .filter((b) => b.tag.includes("aria-expanded") && !b.hasText),
  );

  it("finds the disclosure buttons it is meant to police", () => {
    expect(disclosures.length).toBeGreaterThan(5);
  });

  it("names every button that declares aria-expanded", () => {
    const unnamed = disclosures
      .filter((b) => !NAME_ATTRS.some((a) => b.tag.includes(a)))
      .map((b) => b.file);

    expect(unnamed).toEqual([]);
  });
});
