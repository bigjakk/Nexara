import { describe, it, expect, vi } from "vitest";
import { useState } from "react";
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
});
