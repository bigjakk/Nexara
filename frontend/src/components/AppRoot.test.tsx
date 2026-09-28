import { useState } from "react";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter } from "react-router-dom";
import { describe, expect, it } from "vitest";

import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogTitle,
} from "@/components/ui/dialog";
import { MAIN_CONTENT_ID } from "@/lib/constants";
import { AppRoot } from "./AppRoot";

// A page like AppShell's, whose dialog deletes the row it was opened from and
// names no fallback of its own.
function Page() {
  const [open, setOpen] = useState(false);
  const [deleted, setDeleted] = useState(false);
  return (
    <main id={MAIN_CONTENT_ID} tabIndex={-1}>
      {!deleted && (
        <button
          onClick={() => {
            setOpen(true);
          }}
        >
          Delete linux01
        </button>
      )}
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent>
          <DialogTitle>Delete linux01?</DialogTitle>
          <DialogDescription>It is removed for good.</DialogDescription>
          <button
            onClick={() => {
              setDeleted(true);
              setOpen(false);
            }}
          >
            Delete
          </button>
        </DialogContent>
      </Dialog>
    </main>
  );
}

describe("AppRoot", () => {
  it("puts focus on the page's main content when a dialog's opener has gone", async () => {
    const user = userEvent.setup();
    render(
      <AppRoot
        router={createMemoryRouter([{ path: "/", element: <Page /> }])}
      />,
    );
    await user.click(
      await screen.findByRole("button", { name: "Delete linux01" }),
    );

    await user.click(screen.getByRole("button", { name: "Delete" }));

    await waitFor(() => {
      expect(document.activeElement).toBe(screen.getByRole("main"));
    });
  });
});
