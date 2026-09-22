import { describe, it, expect } from "vitest";
import { screen, within } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { ChangelogDialog } from "./ChangelogDialog";
import type { ChangelogEntry } from "@/lib/changelog";

function entry(over: Partial<ChangelogEntry> = {}): ChangelogEntry {
  return {
    version: "1.13.1",
    date: "2026-09-16",
    highlights: [],
    ...over,
  };
}

function open(entries: ChangelogEntry[]) {
  renderWithProviders(
    <ChangelogDialog open onOpenChange={() => undefined} entries={entries} />,
  );
}

/** The row a title sits in, so a chip can be asserted against its own row. */
function rowOf(title: string): HTMLElement {
  const row = screen.getByText(title).closest("li");
  if (!row) throw new Error(`no row for ${title}`);
  return row;
}

describe("ChangelogDialog highlight rows", () => {
  it("labels each row with the chip for its change type", () => {
    open([
      {
        ...entry(),
        highlights: [
          { title: "Report catalogue", type: "new" },
          { title: "Batch node upserts", type: "improved" },
          { title: "Stop dropping a thing", type: "fix" },
          { title: "Describe the endpoints", type: "docs" },
          { title: "Lock it down", type: "security" },
          { title: "One collection envelope", type: "breaking" },
        ],
      },
    ]);

    const expected: [title: string, chip: string][] = [
      ["Report catalogue", "New"],
      ["Batch node upserts", "Improved"],
      ["Stop dropping a thing", "Fix"],
      ["Describe the endpoints", "Docs"],
      ["Lock it down", "Security"],
      ["One collection envelope", "Breaking"],
    ];
    for (const [title, label] of expected) {
      expect(within(rowOf(title)).getByText(label)).toBeInTheDocument();
    }
  });

  // A release parsed out of a curated "## Highlights" section carries no types,
  // and the chip column has to disappear with them rather than indent every row
  // against an empty gutter.
  it("drops the chip column when no highlight in the entry is typed", () => {
    open([
      {
        ...entry({ version: "1.10.0" }),
        highlights: [
          {
            title: "Veeam Backup & Replication",
            description: "Register a VBR server.",
          },
          {
            title: "Backup coverage",
            description: "One report across providers.",
          },
        ],
      },
    ]);

    const row = rowOf("Veeam Backup & Replication");
    expect(row.className).not.toMatch(/sm:grid/);
    expect(screen.getByText("Register a VBR server.")).toBeInTheDocument();
  });

  // The live v1.13.1 payload is exactly this shape: typed rows from Features /
  // Bug Fixes, plus one untyped row carried over from "Other Changes".
  it("keeps the chip column for an entry that only partly has types", () => {
    open([
      {
        ...entry(),
        highlights: [
          { title: "Report catalogue", type: "new" },
          { title: "Run CI on master pushes" },
        ],
      },
    ]);

    expect(rowOf("Report catalogue").className).toMatch(/sm:grid/);
    const untyped = rowOf("Run CI on master pushes");
    expect(untyped.className).toMatch(/sm:grid/);
    // Its gutter cell stays empty rather than borrowing a neighbour's chip.
    expect(within(untyped).queryByText("New")).not.toBeInTheDocument();
  });

  // The Go and TypeScript vocabularies are pinned to each other by
  // TestChangeTypesMatchTheFrontendUnion, but a server one version ahead can
  // still send a type this build has never heard of. It must not throw.
  it("renders a row with an unrecognised type instead of crashing", () => {
    open([
      {
        ...entry(),
        highlights: [
          {
            title: "Something from the future",
            type: "perf" as unknown as "new",
          },
        ],
      },
    ]);

    expect(screen.getByText("Something from the future")).toBeInTheDocument();
  });

  it("says how many highlights the cap left out", () => {
    open([
      {
        ...entry(),
        highlights: [{ title: "Report catalogue", type: "new" }],
        more_count: 29,
      },
    ]);

    expect(
      screen.getByText("+29 more in the full release notes."),
    ).toBeInTheDocument();
  });
});
