import { createRef, useRef, useState, type ReactNode, type Ref } from "react";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogTitle,
} from "./dialog";
import { FallbackFocusContext } from "./return-focus";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "./tabs";

// Rows whose Delete dialog takes the row, and its button, away. `onDelete`
// may take more away with it: the page, say.
function Rows({ onDelete }: { onDelete?: () => void }) {
  const [rows, setRows] = useState(["linux01", "linux02"]);
  const [pending, setPending] = useState<string | null>(null);
  return (
    <>
      {rows.map((row) => (
        <button
          key={row}
          onClick={() => {
            setPending(row);
          }}
        >
          Delete {row}
        </button>
      ))}
      <Dialog
        open={pending !== null}
        onOpenChange={(open) => {
          if (!open) setPending(null);
        }}
      >
        <DialogContent>
          <DialogTitle>Delete {pending}?</DialogTitle>
          <DialogDescription>It is removed for good.</DialogDescription>
          <button
            onClick={() => {
              setRows((all) => all.filter((row) => row !== pending));
              setPending(null);
              onDelete?.();
            }}
          >
            Delete
          </button>
        </DialogContent>
      </Dialog>
    </>
  );
}

function GuestTabs({
  children,
  ref,
}: {
  children: ReactNode;
  ref?: Ref<HTMLDivElement> | undefined;
}) {
  return (
    <Tabs ref={ref} defaultValue="guests">
      <TabsList>
        <TabsTrigger value="guests">Guests</TabsTrigger>
        <TabsTrigger value="storage">Storage</TabsTrigger>
      </TabsList>
      <TabsContent value="guests">{children}</TabsContent>
      <TabsContent value="storage">Pools</TabsContent>
    </Tabs>
  );
}

async function deleteLinux01() {
  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: "Delete linux01" }));
  await user.click(screen.getByRole("button", { name: "Delete" }));
}

describe("Tabs as a place for a dialog's focus to go", () => {
  it("takes focus when a dialog in the panel on show has lost its opener", async () => {
    render(
      <GuestTabs>
        <Rows />
      </GuestTabs>,
    );

    await deleteLinux01();

    await waitFor(() => {
      expect(document.activeElement).toBe(
        screen.getByRole("tabpanel", { name: "Guests" }),
      );
    });
  });

  it("offers the innermost panel with tabs inside tabs", async () => {
    render(
      <Tabs defaultValue="firewall">
        <TabsList>
          <TabsTrigger value="firewall">Firewall</TabsTrigger>
          <TabsTrigger value="ha">HA</TabsTrigger>
        </TabsList>
        <TabsContent value="firewall">
          <GuestTabs>
            <Rows />
          </GuestTabs>
        </TabsContent>
        <TabsContent value="ha">Resources</TabsContent>
      </Tabs>,
    );

    await deleteLinux01();

    await waitFor(() => {
      expect(document.activeElement).toBe(
        screen.getByRole("tabpanel", { name: "Guests" }),
      );
    });
  });

  it("offers their own panel to a dialog beside tabs inside it", async () => {
    render(
      <Tabs defaultValue="firewall">
        <TabsList>
          <TabsTrigger value="firewall">Firewall</TabsTrigger>
          <TabsTrigger value="ha">HA</TabsTrigger>
        </TabsList>
        <TabsContent value="firewall">
          <Rows />
          <GuestTabs>Rules</GuestTabs>
        </TabsContent>
        <TabsContent value="ha">Resources</TabsContent>
      </Tabs>,
    );

    await deleteLinux01();

    await waitFor(() => {
      expect(document.activeElement).toBe(
        screen.getByRole("tabpanel", { name: "Firewall" }),
      );
    });
  });

  // The page the tabs were on has gone — a delete that navigates away.
  function Page() {
    const [gone, setGone] = useState(false);
    const main = useRef<HTMLElement>(null);
    return (
      <FallbackFocusContext value={[() => main.current]}>
        <main ref={main} tabIndex={-1}>
          {!gone && (
            <GuestTabs>
              <Rows
                onDelete={() => {
                  setGone(true);
                }}
              />
            </GuestTabs>
          )}
        </main>
      </FallbackFocusContext>
    );
  }

  it("hands on to the region around them once they have gone", async () => {
    render(<Page />);

    await deleteLinux01();

    await waitFor(() => {
      expect(document.activeElement).toBe(screen.getByRole("main"));
    });
  });
});

describe("A ref on Tabs", () => {
  it("is given the root", () => {
    const ref = createRef<HTMLDivElement>();

    render(<GuestTabs ref={ref}>Guests</GuestTabs>);

    expect(ref.current).toContainElement(screen.getByRole("tablist"));
  });

  // React 19 runs a callback ref's cleanup in place of calling it with null.
  it("keeps the cleanup a callback ref returns", () => {
    const seen: (HTMLDivElement | null)[] = [];
    let cleanups = 0;
    const { unmount } = render(
      <GuestTabs
        ref={(node) => {
          seen.push(node);
          return () => {
            cleanups += 1;
          };
        }}
      >
        Guests
      </GuestTabs>,
    );

    unmount();

    expect(cleanups).toBe(1);
    expect(seen).toHaveLength(1);
    expect(seen[0]).toBeInstanceOf(HTMLDivElement);
  });
});
