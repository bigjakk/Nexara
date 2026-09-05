/// <reference types="vite/client" />
import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";

import { Dialog, DialogContent } from "@/components/ui/dialog";
import { cn } from "@/lib/utils";

/**
 * Guard tests for the dialog primitives, in the spirit of
 * internal/api/handlers/tracktask_guard_test.go — encode the invariant so the
 * next person can't quietly break it.
 *
 * DialogContent/AlertDialogContent carry `max-h-[85vh] overflow-y-auto` in
 * their base class string, so any dialog taller than the viewport scrolls
 * instead of spilling off-screen unreachably (it is fixed-position and centred
 * via translate-y-[-50%], so there is nothing to scroll without this).
 *
 * jsdom does no layout and loads no CSS, so we cannot assert that a dialog
 * actually scrolls. What we CAN pin down are the two things that silently
 * un-fix all ~138 call sites: the tailwind-merge conflict groups, and call
 * sites that disable the scroll without capping their own height.
 */

// Vite resolves a leading-slash glob against its root (frontend/), so this can
// never reach the stale copies of the tree that live in git worktrees under
// .claude/worktrees/.
const sources: Record<string, string> = import.meta.glob("/src/**/*.tsx", {
  eager: true,
  query: "?raw",
  import: "default",
});

const OPENING_TAG = /<(?:Alert)?DialogContent\b/g;

/**
 * Return the full opening tag starting at `start`, e.g. `<DialogContent … >`.
 *
 * A plain `/<DialogContent[^>]*>/` is not good enough: it stops at the first
 * `>` even when that `>` belongs to a string (`aria-label="a > b"`) or to an
 * arrow-function prop (`onEscapeKeyDown={(e) => …}`), which would silently drop
 * the rest of the tag — and with it the className we came to inspect. Track
 * quotes and nesting so we stop at the real tag end.
 */
function readOpeningTag(source: string, start: number): string {
  let depth = 0;
  let quote: string | null = null;

  for (let i = start; i < source.length; i++) {
    const ch = source[i];

    if (quote !== null) {
      if (ch === "\\") i++;
      else if (ch === quote) quote = null;
      continue;
    }
    if (ch === '"' || ch === "'" || ch === "`") quote = ch;
    else if (ch === "{" || ch === "(") depth++;
    else if (ch === "}" || ch === ")") depth--;
    else if (ch === ">" && depth === 0) return source.slice(start, i + 1);
  }
  return source.slice(start);
}

interface CallSite {
  path: string;
  line: number;
  tag: string;
}

function callSites(): CallSite[] {
  const sites: CallSite[] = [];
  for (const [path, source] of Object.entries(sources)) {
    for (const match of source.matchAll(OPENING_TAG)) {
      sites.push({
        path,
        line: source.slice(0, match.index).split("\n").length,
        tag: readOpeningTag(source, match.index),
      });
    }
  }
  return sites;
}

describe("DialogContent base classes", () => {
  it("caps height and scrolls, so tall dialogs are never unreachable", () => {
    render(
      <Dialog open>
        <DialogContent>
          <p>body</p>
        </DialogContent>
      </Dialog>,
    );
    const content = screen.getByText("body").parentElement;
    expect(content).toHaveClass("max-h-[85vh]");
    expect(content).toHaveClass("overflow-y-auto");
  });

  // If a tailwind-merge upgrade ever stops treating `overflow` as conflicting
  // with `overflow-y`, every dialog that sets overflow-hidden would keep the
  // base overflow-y-auto and grow a second scrollbar. Pin the semantics we
  // rely on rather than trusting them.
  it("lets a call site's own overflow / max-h win over the base", () => {
    const base = "grid max-h-[85vh] overflow-y-auto p-6";
    expect(cn(base, "overflow-hidden")).not.toContain("overflow-y-auto");
    expect(cn(base, "max-h-[90vh]")).not.toContain("max-h-[85vh]");
    expect(cn(base, "max-h-[90vh]")).toContain("max-h-[90vh]");
  });
});

describe("DialogContent call sites", () => {
  /**
   * `overflow-*` and `max-h-*` are independent tailwind-merge groups, so a call
   * site that sets only `overflow-hidden` strips the base scroll but KEEPS the
   * base 85vh cap — content past the cap is then clipped with no scrollbar at
   * all, which is how MigrateJobDialog's submit button became unreachable.
   *
   * If you disable the scroll you own the height: declare an explicit `max-h-*`
   * AND give the dialog an inner scroll region. Only the first half is
   * mechanically checkable here; the inner scroller is on you.
   */
  it("never disables scrolling without declaring a height cap", () => {
    const offenders = callSites()
      .filter(({ tag }) => /overflow-/.test(tag) && !/max-h-/.test(tag))
      .map(
        ({ path, line, tag }) =>
          `${path}:${String(line)} → ${tag.replace(/\s+/g, " ")}`,
      );

    expect(offenders).toEqual([]);
  });

  /**
   * Canaries. The check above is a filter over `callSites()`, so it passes
   * vacuously if the glob stops resolving or `readOpeningTag` starts returning
   * truncated tags. Assert on the shape of what was actually scanned — both the
   * total and the className-bearing subset the filter really depends on.
   */
  it("scans a plausible number of call sites", () => {
    const sites = callSites();
    expect(sites.length).toBeGreaterThan(100);

    const withClassName = sites.filter(({ tag }) => tag.includes("className"));
    expect(withClassName.length).toBeGreaterThan(50);

    // A truncated tag is the silent failure mode: it still contains
    // `<DialogContent` but loses the className that follows.
    expect(sites.every(({ tag }) => tag.endsWith(">"))).toBe(true);
  });
});
