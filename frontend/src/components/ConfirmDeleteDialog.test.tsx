import { describe, it, expect, vi } from "vitest";
import { StrictMode, useState, type ReactElement } from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ConfirmDeleteDialog } from "./ConfirmDeleteDialog";

interface Item {
  name: string;
}

function Harness({
  onConfirm,
  onClose = () => undefined,
  items = [{ name: "alpha" }, { name: "beta" }],
}: {
  onConfirm: (item: Item) => void;
  onClose?: () => void;
  items?: Item[];
}) {
  const [pending, setPending] = useState<Item | null>(null);
  return (
    <>
      {items.map((it) => (
        <button
          key={it.name}
          onClick={() => {
            setPending(it);
          }}
        >
          Delete {it.name}
        </button>
      ))}
      <ConfirmDeleteDialog
        target={pending}
        onClose={() => {
          onClose();
          setPending(null);
        }}
        onConfirm={onConfirm}
        title={(it) => `Delete item ${it.name}?`}
        description={(it) => `Item ${it.name} is removed for good.`}
        confirmLabel="Delete Item"
      />
    </>
  );
}

describe("ConfirmDeleteDialog", () => {
  it("opens on the row's button, naming that row, and sends nothing yet", async () => {
    const onConfirm = vi.fn();
    render(<Harness onConfirm={onConfirm} />);
    expect(screen.queryByRole("alertdialog")).toBeNull();

    await userEvent.click(screen.getByRole("button", { name: "Delete beta" }));

    const dialog = screen.getByRole("alertdialog");
    expect(dialog).toHaveTextContent("Delete item beta?");
    expect(dialog).toHaveTextContent("Item beta is removed for good.");
    expect(onConfirm).not.toHaveBeenCalled();
  });

  it("Cancel closes it without confirming", async () => {
    const onConfirm = vi.fn();
    render(<Harness onConfirm={onConfirm} />);
    await userEvent.click(screen.getByRole("button", { name: "Delete alpha" }));

    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));

    expect(onConfirm).not.toHaveBeenCalled();
    expect(
      screen.queryByRole("alertdialog", { hidden: false }),
    ).not.toBeInTheDocument();
  });

  it("Escape closes it without confirming", async () => {
    const onConfirm = vi.fn();
    render(<Harness onConfirm={onConfirm} />);
    await userEvent.click(screen.getByRole("button", { name: "Delete alpha" }));

    await userEvent.keyboard("{Escape}");

    expect(onConfirm).not.toHaveBeenCalled();
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
  });

  it("confirm deletes exactly the row it was opened for, once, and closes", async () => {
    const onConfirm = vi.fn();
    render(<Harness onConfirm={onConfirm} />);
    await userEvent.click(screen.getByRole("button", { name: "Delete beta" }));

    await userEvent.click(screen.getByRole("button", { name: "Delete Item" }));

    expect(onConfirm).toHaveBeenCalledTimes(1);
    expect(onConfirm).toHaveBeenCalledWith({ name: "beta" });
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
  });

  it("confirm closes the dialog through onClose exactly once", async () => {
    const onClose = vi.fn();
    render(<Harness onConfirm={vi.fn()} onClose={onClose} />);
    await userEvent.click(screen.getByRole("button", { name: "Delete beta" }));

    await userEvent.click(screen.getByRole("button", { name: "Delete Item" }));

    expect(onClose).toHaveBeenCalledTimes(1);
  });

  // Opened by state with no Trigger, Radix alone would drop focus on <body>.
  // Each case runs plain and under StrictMode, whose double render and effect
  // replay must not record an element inside the dialog as the opener.
  const CLOSES = [
    { how: "Cancel", close: () => userEvent.click(screen.getByText("Cancel")) },
    { how: "Escape", close: () => userEvent.keyboard("{Escape}") },
    {
      how: "confirm",
      close: () => userEvent.click(screen.getByText("Delete Item")),
    },
  ];
  const MODES = [
    { mode: "plain", wrap: (ui: ReactElement) => ui },
    {
      mode: "StrictMode",
      wrap: (ui: ReactElement) => <StrictMode>{ui}</StrictMode>,
    },
  ];
  const CASES = MODES.flatMap((m) => CLOSES.map((c) => ({ ...m, ...c })));

  it.each(CASES)(
    "$how returns focus to the button that opened it ($mode)",
    async ({ wrap, close }) => {
      render(wrap(<Harness onConfirm={vi.fn()} />));
      const opener = screen.getByRole("button", { name: "Delete beta" });
      await userEvent.click(opener);
      expect(document.activeElement).not.toBe(opener);

      await close();

      expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
      expect(document.activeElement).toBe(opener);
    },
  );

  it.each(CASES)(
    "$how returns focus to the opener after the parent re-rendered while open ($mode)",
    async ({ wrap, close }) => {
      const { rerender } = render(wrap(<Harness onConfirm={vi.fn()} />));
      const opener = screen.getByRole("button", { name: "Delete beta" });
      await userEvent.click(opener);
      // Focus is inside the dialog now; a re-render must not take it as the
      // opener.
      expect(screen.getByRole("alertdialog")).toContainElement(
        document.activeElement as HTMLElement,
      );
      rerender(wrap(<Harness onConfirm={vi.fn()} />));

      await close();

      expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
      expect(document.activeElement).toBe(opener);
    },
  );

  it.each(CASES)(
    "$how returns focus to the second opener when reopened from another row ($mode)",
    async ({ wrap, close }) => {
      render(wrap(<Harness onConfirm={vi.fn()} />));
      const first = screen.getByRole("button", { name: "Delete alpha" });
      const second = screen.getByRole("button", { name: "Delete beta" });
      await userEvent.click(first);
      await userEvent.click(screen.getByText("Cancel"));
      expect(document.activeElement).toBe(first);

      await userEvent.click(second);
      await close();

      expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
      expect(document.activeElement).toBe(second);
    },
  );
});
