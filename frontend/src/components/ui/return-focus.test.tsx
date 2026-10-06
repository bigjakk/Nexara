import {
  StrictMode,
  useCallback,
  useEffect,
  useRef,
  useState,
  type ReactElement,
  type ReactNode,
} from "react";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import { ConfirmDeleteDialog } from "@/components/ConfirmDeleteDialog";

import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogTitle,
} from "./alert-dialog";
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuTrigger,
} from "./context-menu";
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogTitle,
  DialogTrigger,
} from "./dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "./dropdown-menu";
import {
  FallbackFocusContext,
  resolveOpener,
  useFallbackFocus,
  type FallbackFocus,
} from "./return-focus";
import { Sheet, SheetContent, SheetDescription, SheetTitle } from "./sheet";

type User = ReturnType<typeof userEvent.setup>;

interface ModalProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  fallbackFocus?: FallbackFocus | undefined;
  onCloseAutoFocus?: ((event: Event) => void) | undefined;
  children?: ReactNode;
}

// Each wrapper, opened by state with no Trigger: the case Radix left on <body>.
function DialogModal(props: ModalProps) {
  return (
    <Dialog open={props.open} onOpenChange={props.onOpenChange}>
      <DialogContent
        fallbackFocus={props.fallbackFocus}
        onCloseAutoFocus={(event) => {
          props.onCloseAutoFocus?.(event);
        }}
      >
        <DialogTitle>Edit linux01</DialogTitle>
        <DialogDescription>Change its settings.</DialogDescription>
        {props.children}
      </DialogContent>
    </Dialog>
  );
}

function AlertModal(props: ModalProps) {
  return (
    <AlertDialog open={props.open} onOpenChange={props.onOpenChange}>
      <AlertDialogContent
        fallbackFocus={props.fallbackFocus}
        onCloseAutoFocus={(event) => {
          props.onCloseAutoFocus?.(event);
        }}
      >
        <AlertDialogTitle>Delete linux01?</AlertDialogTitle>
        <AlertDialogDescription>It is removed for good.</AlertDialogDescription>
        {props.children}
        <AlertDialogCancel>Cancel</AlertDialogCancel>
      </AlertDialogContent>
    </AlertDialog>
  );
}

function SheetModal(props: ModalProps) {
  return (
    <Sheet open={props.open} onOpenChange={props.onOpenChange}>
      <SheetContent
        fallbackFocus={props.fallbackFocus}
        onCloseAutoFocus={(event) => {
          props.onCloseAutoFocus?.(event);
        }}
      >
        <SheetTitle>Navigation</SheetTitle>
        <SheetDescription>Pages and resources.</SheetDescription>
        {props.children}
      </SheetContent>
    </Sheet>
  );
}

// A click handler that sets a boolean state.
const set = (setter: (value: boolean) => void, value: boolean) => () => {
  setter(value);
};

type Modal = (props: ModalProps) => ReactElement;

interface Kind {
  kind: string;
  role: "dialog" | "alertdialog";
  Modal: Modal;
}

// The three wrappers wire the shared hooks one by one, so each gets the tests
// of that wiring; everything the hooks decide is run through Dialog alone.
const DIALOG: Kind = { kind: "Dialog", role: "dialog", Modal: DialogModal };
const KINDS: readonly Kind[] = [
  DIALOG,
  { kind: "AlertDialog", role: "alertdialog", Modal: AlertModal },
  { kind: "Sheet", role: "dialog", Modal: SheetModal },
];

// StrictMode's replay of the root's effect and of the content's ref happens as
// a dialog mounts already open; it must not record an element inside the
// dialog as the opener.
const MODES = [
  { mode: "plain", wrap: (ui: ReactElement) => ui },
  {
    mode: "StrictMode",
    wrap: (ui: ReactElement) => <StrictMode>{ui}</StrictMode>,
  },
];

function overlay(): HTMLElement {
  const el = document.querySelector<HTMLElement>(
    '.fixed.inset-0[data-state="open"]',
  );
  if (el === null) throw new Error("no open overlay");
  return el;
}

// Radix returns focus on a timer once the content has gone; long enough for
// that, and for anything queued behind it.
async function timersRun() {
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 30));
  });
}

function button(name: string): HTMLElement {
  return screen.getByRole("button", { name });
}

function region(name: string): HTMLElement {
  return screen.getByRole("region", { name });
}

/**
 * An Edit button that opens the dialog, another button, and a card to fall
 * back to. Save closes the dialog by state; `afterSave` says what else it
 * does, `brokenFallback` swaps in a fallback that cannot take focus, and the
 * other props are for re-rendering the page after a close.
 */
function Page({
  Modal,
  afterSave = "nothing",
  withFallback = false,
  brokenFallback,
  openerShown = true,
  openerDisabled = false,
  elsewhereShown = true,
  autoFocusInput = false,
  onCloseAutoFocus,
}: {
  Modal: Modal;
  afterSave?: "nothing" | "disable" | "remove" | "focus-elsewhere";
  withFallback?: boolean;
  brokenFallback?: "gone" | "unfocusable";
  openerShown?: boolean;
  openerDisabled?: boolean;
  elsewhereShown?: boolean;
  autoFocusInput?: boolean;
  onCloseAutoFocus?: (event: Event) => void;
}) {
  const [open, setOpen] = useState(false);
  const [saved, setSaved] = useState(false);
  const card = useRef<HTMLDivElement>(null);
  const plain = useRef<HTMLDivElement>(null);
  const elsewhere = useRef<HTMLButtonElement>(null);
  const shown = openerShown && !(saved && afterSave === "remove");
  let fallbackFocus: FallbackFocus | undefined;
  if (brokenFallback === "gone") fallbackFocus = () => null;
  else if (brokenFallback === "unfocusable")
    fallbackFocus = () => plain.current;
  else if (withFallback) fallbackFocus = () => card.current;
  const disabled = openerDisabled || (saved && afterSave === "disable");
  return (
    <>
      {shown && (
        <button
          disabled={disabled}
          onClick={() => {
            setOpen(true);
          }}
        >
          Edit
        </button>
      )}
      {elsewhereShown && <button ref={elsewhere}>Elsewhere</button>}
      <div ref={card} role="region" aria-label="Card" tabIndex={-1}>
        Card
      </div>
      <div ref={plain}>Plain</div>
      <Modal
        open={open}
        onOpenChange={setOpen}
        fallbackFocus={fallbackFocus}
        onCloseAutoFocus={onCloseAutoFocus}
      >
        {autoFocusInput && <input aria-label="Name" autoFocus />}
        <button
          onClick={() => {
            setSaved(true);
            setOpen(false);
            if (afterSave === "focus-elsewhere") {
              setTimeout(() => {
                elsewhere.current?.focus();
              }, 0);
            }
          }}
        >
          Save
        </button>
      </Modal>
    </>
  );
}

afterEach(() => {
  // Ends any watch a test left armed.
  fireEvent.pointerDown(document);
});

describe("a dialog opened by state", () => {
  // Every way of closing ends in the same onCloseAutoFocus, so only Dialog
  // takes them all; an AlertDialog does not close on an outside click.
  const closings = [
    ...KINDS.map((k) => ({
      ...k,
      how: "Escape",
      close: (user: User) => user.keyboard("{Escape}"),
    })),
    {
      ...DIALOG,
      how: "Save",
      close: (user: User) => user.click(button("Save")),
    },
    {
      ...DIALOG,
      how: "an outside click",
      close: (user: User) => user.click(overlay()),
    },
  ];

  it.each(closings)(
    "$how returns focus to the button that opened the $kind",
    async ({ Modal, role, close }) => {
      const user = userEvent.setup();
      render(<Page Modal={Modal} />);
      const opener = button("Edit");
      await user.click(opener);
      expect(screen.getByRole(role)).toContainElement(
        document.activeElement as HTMLElement,
      );

      await close(user);

      expect(screen.queryByRole(role)).not.toBeInTheDocument();
      await waitFor(() => {
        expect(document.activeElement).toBe(opener);
      });
    },
  );

  function MountedWhileOpen() {
    const [open, setOpen] = useState(false);
    return (
      <>
        <button onClick={set(setOpen, true)}>Edit</button>
        {open && (
          <DialogModal open onOpenChange={setOpen}>
            <button onClick={set(setOpen, false)}>Save</button>
          </DialogModal>
        )}
      </>
    );
  }

  it.each(MODES)(
    "returns focus when the Dialog is mounted only while open ($mode)",
    async ({ wrap }) => {
      const user = userEvent.setup();
      render(wrap(<MountedWhileOpen />));
      const opener = button("Edit");

      await user.click(opener);
      await user.keyboard("{Escape}");
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
      await waitFor(() => {
        expect(document.activeElement).toBe(opener);
      });

      await user.click(opener);
      await user.click(button("Save"));
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
      await waitFor(() => {
        expect(document.activeElement).toBe(opener);
      });
    },
  );

  function TwoOpeners() {
    const [open, setOpen] = useState(false);
    return (
      <>
        {["Edit A", "Edit B"].map((name) => (
          <button
            key={name}
            onClick={() => {
              setOpen(true);
            }}
          >
            {name}
          </button>
        ))}
        <DialogModal open={open} onOpenChange={setOpen} />
      </>
    );
  }

  it("records a new opener each time it opens", async () => {
    const user = userEvent.setup();
    render(<TwoOpeners />);
    const first = button("Edit A");
    const second = button("Edit B");
    await user.click(first);
    await user.keyboard("{Escape}");
    await waitFor(() => {
      expect(document.activeElement).toBe(first);
    });

    await user.click(second);
    await user.keyboard("{Escape}");

    await waitFor(() => {
      expect(document.activeElement).toBe(second);
    });
  });

  // FocusScope does not dispatch onOpenAutoFocus when focus is already
  // inside the content, which is where an autoFocus input puts it.
  it("returns focus from a dialog whose input takes focus itself", async () => {
    const user = userEvent.setup();
    render(<Page Modal={DialogModal} autoFocusInput />);
    const opener = button("Edit");
    await user.click(opener);
    expect(screen.getByRole("textbox", { name: "Name" })).toHaveFocus();

    await user.keyboard("{Escape}");

    await waitFor(() => {
      expect(document.activeElement).toBe(opener);
    });
  });
});

describe("when the opener cannot take focus back", () => {
  it.each(KINDS)(
    "puts focus on the fallback when Save disabled the opener ($kind)",
    async ({ Modal }) => {
      const user = userEvent.setup();
      render(<Page Modal={Modal} afterSave="disable" withFallback />);
      await user.click(button("Edit"));

      await user.click(button("Save"));

      await waitFor(() => {
        expect(document.activeElement).toBe(region("Card"));
      });
      expect(button("Edit")).toBeDisabled();
    },
  );

  it("puts focus on the fallback when Save removed the opener", async () => {
    const user = userEvent.setup();
    render(<Page Modal={DialogModal} afterSave="remove" withFallback />);
    const focus = vi.spyOn(region("Card"), "focus");
    await user.click(button("Edit"));

    await user.click(button("Save"));

    await waitFor(() => {
      expect(document.activeElement).toBe(region("Card"));
    });
    expect(screen.queryByRole("button", { name: "Edit" })).toBeNull();
    // Scrolled to: it is where the dialog's opener was a moment ago.
    expect(focus).toHaveBeenCalledWith(undefined);
  });

  // A click that does not focus the button it lands on — Safari's, say —
  // leaves no opener to go back to.
  it("puts focus on the fallback when nothing had focus as the dialog opened", async () => {
    const user = userEvent.setup();
    render(<Page Modal={DialogModal} withFallback />);
    fireEvent.click(button("Edit"));

    await user.keyboard("{Escape}");

    await waitFor(() => {
      expect(document.activeElement).toBe(region("Card"));
    });
  });

  function InSection(
    props: Partial<Omit<Parameters<typeof Page>[0], "Modal">>,
  ) {
    const section = useRef<HTMLDivElement>(null);
    return (
      <FallbackFocusContext value={[() => section.current]}>
        <div ref={section} role="region" aria-label="Section" tabIndex={-1}>
          <Page Modal={DialogModal} afterSave="remove" {...props} />
        </div>
      </FallbackFocusContext>
    );
  }

  it("puts focus on the FallbackFocusContext element without a fallback of its own", async () => {
    const user = userEvent.setup();
    render(<InSection />);
    await user.click(button("Edit"));

    await user.click(button("Save"));

    await waitFor(() => {
      expect(document.activeElement).toBe(region("Section"));
    });
  });

  it("prefers its own fallbackFocus to the FallbackFocusContext", async () => {
    const user = userEvent.setup();
    render(<InSection withFallback />);
    await user.click(button("Edit"));

    await user.click(button("Save"));

    await waitFor(() => {
      expect(document.activeElement).toBe(region("Card"));
    });
  });

  it.each([{ broken: "gone" as const }, { broken: "unfocusable" as const }])(
    "hands on to the regions around it when its own fallback is $broken",
    async ({ broken }) => {
      const user = userEvent.setup();
      render(<InSection brokenFallback={broken} />);
      await user.click(button("Edit"));

      await user.click(button("Save"));

      await waitFor(() => {
        expect(document.activeElement).toBe(region("Section"));
      });
    },
  );

  // A region that has nothing to offer by then: a page navigated away from.
  function EmptyRegion({ children }: { children: ReactNode }) {
    const nothing = useCallback(() => null, []);
    const fallbacks = useFallbackFocus(nothing);
    return (
      <FallbackFocusContext value={fallbacks}>{children}</FallbackFocusContext>
    );
  }

  it("hands on from a region with nothing to offer to the one around it", async () => {
    const user = userEvent.setup();
    const section = document.createElement("div");
    section.tabIndex = -1;
    document.body.append(section);
    try {
      render(
        <FallbackFocusContext value={[() => section]}>
          <EmptyRegion>
            <Page Modal={DialogModal} afterSave="remove" />
          </EmptyRegion>
        </FallbackFocusContext>,
      );
      await user.click(button("Edit"));

      await user.click(button("Save"));

      await waitFor(() => {
        expect(document.activeElement).toBe(section);
      });
    } finally {
      section.remove();
    }
  });

  it("leaves focus alone with no fallback, and never focuses <body>", async () => {
    const bodyFocus = vi.spyOn(document.body, "focus");
    const user = userEvent.setup();
    render(<Page Modal={DialogModal} afterSave="remove" />);
    await user.click(button("Edit"));

    await user.click(button("Save"));
    await timersRun();

    expect(document.activeElement).toBe(document.body);
    expect(bodyFocus).not.toHaveBeenCalled();
    bodyFocus.mockRestore();
  });
});

// The row a confirmed delete removes is usually still there when its dialog
// closes, and goes a moment later, when the list is read again.
describe("after focus has gone back to the opener", () => {
  async function closedBackOnOpener(user: User) {
    const opener = button("Edit");
    await user.click(opener);
    await user.click(button("Save"));
    await waitFor(() => {
      expect(document.activeElement).toBe(opener);
    });
  }

  // Without scrolling: the user may have scrolled away from the fallback by then.
  it("moves focus to the fallback, without scrolling to it, when the opener is then removed", async () => {
    const user = userEvent.setup();
    const { rerender } = render(<Page Modal={DialogModal} withFallback />);
    await closedBackOnOpener(user);
    const card = region("Card");
    const focus = vi.spyOn(card, "focus");

    rerender(<Page Modal={DialogModal} withFallback openerShown={false} />);

    await waitFor(() => {
      expect(document.activeElement).toBe(card);
    });
    expect(focus).toHaveBeenCalledWith({ preventScroll: true });
  });

  it("stops watching after a while", async () => {
    const user = userEvent.setup();
    const { rerender } = render(<Page Modal={DialogModal} withFallback />);
    await closedBackOnOpener(user);
    const later = vi.spyOn(Date, "now").mockReturnValue(Date.now() + 31_000);
    try {
      rerender(<Page Modal={DialogModal} withFallback openerShown={false} />);
      await timersRun();

      expect(document.activeElement).toBe(document.body);
    } finally {
      later.mockRestore();
    }
  });

  it("moves focus to the fallback when the opener is then disabled", async () => {
    const user = userEvent.setup();
    const { rerender } = render(<Page Modal={DialogModal} withFallback />);
    await closedBackOnOpener(user);

    rerender(<Page Modal={DialogModal} withFallback openerDisabled />);

    await waitFor(() => {
      expect(document.activeElement).toBe(region("Card"));
    });
  });

  it("stops watching once focus has moved elsewhere", async () => {
    const user = userEvent.setup();
    const { rerender } = render(<Page Modal={DialogModal} withFallback />);
    await closedBackOnOpener(user);
    act(() => {
      button("Elsewhere").focus();
    });

    // Focus falls to <body> with the button it had moved to.
    rerender(
      <Page
        Modal={DialogModal}
        withFallback
        openerShown={false}
        elsewhereShown={false}
      />,
    );
    await timersRun();

    expect(document.activeElement).toBe(document.body);
  });

  it("stops watching at a pointerdown", async () => {
    const user = userEvent.setup();
    const { rerender } = render(<Page Modal={DialogModal} withFallback />);
    await closedBackOnOpener(user);
    fireEvent.pointerDown(screen.getByText("Card"));

    rerender(<Page Modal={DialogModal} withFallback openerShown={false} />);
    await timersRun();

    expect(document.activeElement).toBe(document.body);
  });
});

describe("in a window that does not have focus", () => {
  function TwoOpenersWithFallback() {
    const [open, setOpen] = useState(false);
    const card = useRef<HTMLDivElement>(null);
    return (
      <>
        {["Edit A", "Edit B"].map((name) => (
          <button
            key={name}
            onClick={() => {
              setOpen(true);
            }}
          >
            {name}
          </button>
        ))}
        <div ref={card} role="region" aria-label="Card" tabIndex={-1}>
          Card
        </div>
        <DialogModal
          open={open}
          onOpenChange={setOpen}
          fallbackFocus={() => card.current}
        >
          <button onClick={set(setOpen, false)}>Save</button>
        </DialogModal>
      </>
    );
  }

  // There focus() fires no focusin, so a watch is not ended by focus moving
  // on; left armed, it would take the focus the next dialog's content drops
  // to <body> as it unmounts.
  it("ends the last dialog's watch when the next one opens", async () => {
    const swallow = (event: FocusEvent) => {
      event.stopImmediatePropagation();
    };
    window.addEventListener("focusin", swallow, true);
    try {
      const user = userEvent.setup();
      render(<TwoOpenersWithFallback />);
      const first = button("Edit A");
      const second = button("Edit B");
      await user.click(first);
      await user.click(button("Save"));
      await waitFor(() => {
        expect(document.activeElement).toBe(first);
      });

      // By keyboard: a click's pointerdown would end the watch itself.
      act(() => {
        second.focus();
      });
      await user.keyboard("{Enter}");
      expect(screen.getByRole("dialog")).toBeInTheDocument();
      await user.keyboard("{Escape}");

      await waitFor(() => {
        expect(document.activeElement).toBe(second);
      });
      await timersRun();
      expect(document.activeElement).toBe(second);
    } finally {
      window.removeEventListener("focusin", swallow, true);
    }
  });
});

describe("focus placed on purpose", () => {
  it("lets an onCloseAutoFocus that calls preventDefault() decide, even to leave focus alone", async () => {
    const user = userEvent.setup();
    render(
      <Page
        Modal={DialogModal}
        withFallback
        onCloseAutoFocus={(event) => {
          event.preventDefault();
        }}
      />,
    );
    await user.click(button("Edit"));

    await user.click(button("Save"));
    await timersRun();

    expect(document.activeElement).toBe(document.body);
  });

  it("leaves focus where the app put it before the dialog finished closing", async () => {
    const user = userEvent.setup();
    render(
      <Page Modal={DialogModal} withFallback afterSave="focus-elsewhere" />,
    );
    await user.click(button("Edit"));

    await user.click(button("Save"));
    await timersRun();

    expect(document.activeElement).toBe(button("Elsewhere"));
  });
});

describe("a non-modal dialog", () => {
  function NonModal() {
    const [open, setOpen] = useState(false);
    return (
      <>
        <button onClick={set(setOpen, true)}>Edit</button>
        <p>Outside text</p>
        <Dialog modal={false} open={open} onOpenChange={setOpen}>
          <DialogContent>
            <DialogTitle>Details</DialogTitle>
            <DialogDescription>Read only.</DialogDescription>
          </DialogContent>
        </Dialog>
      </>
    );
  }

  it("leaves focus where an outside click put it", async () => {
    const user = userEvent.setup();
    render(<NonModal />);
    await user.click(button("Edit"));
    expect(screen.getByRole("dialog")).toBeInTheDocument();

    await user.click(screen.getByText("Outside text"));
    await timersRun();

    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(document.activeElement).not.toBe(button("Edit"));
  });
});

describe("a dialog opened from a menu", () => {
  function FromDropdown({ via }: { via: "onSelect" | "onClick" }) {
    const [open, setOpen] = useState(false);
    const openIt = () => {
      setOpen(true);
    };
    return (
      <>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <button>Actions for linux01</button>
          </DropdownMenuTrigger>
          <DropdownMenuContent>
            <DropdownMenuItem
              {...(via === "onSelect"
                ? { onSelect: openIt }
                : { onClick: openIt })}
            >
              Edit
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
        <DialogModal open={open} onOpenChange={setOpen} />
      </>
    );
  }

  // The item goes with the menu; the menu's own button is the one to go back to.
  it.each(["onSelect", "onClick"] as const)(
    "returns focus to the dropdown's button, not its gone item (%s)",
    async (via) => {
      const user = userEvent.setup();
      render(<FromDropdown via={via} />);
      const trigger = button("Actions for linux01");
      await user.click(trigger);
      await user.click(await screen.findByRole("menuitem", { name: "Edit" }));
      expect(await screen.findByRole("dialog")).toBeInTheDocument();

      await user.keyboard("{Escape}");

      await waitFor(() => {
        expect(document.activeElement).toBe(trigger);
      });
      expect(screen.queryByRole("menu")).not.toBeInTheDocument();
    },
  );

  // Inside a focusable <main>, as in AppShell: it is around every row too.
  function FromContextMenu() {
    const [open, setOpen] = useState(false);
    return (
      <main tabIndex={-1}>
        <button>Elsewhere</button>
        <ContextMenu modal={false}>
          <ContextMenuTrigger asChild>
            <div>
              <button>linux01</button> <span>running</span>
            </div>
          </ContextMenuTrigger>
          <ContextMenuContent>
            <ContextMenuItem onClick={set(setOpen, true)}>
              Clone
            </ContextMenuItem>
          </ContextMenuContent>
        </ContextMenu>
        <Dialog open={open} onOpenChange={setOpen}>
          <DialogContent>
            <DialogTitle>Clone linux01</DialogTitle>
            <DialogDescription>Make a copy.</DialogDescription>
          </DialogContent>
        </Dialog>
      </main>
    );
  }

  // Three ways to open the menu, each remembering what it was opened on
  // differently; all lead back to the row's button.
  const openings: [how: string, open: (user: User) => unknown][] = [
    [
      "with the mouse, on the row's text rather than its button",
      (user) => {
        button("Elsewhere").focus();
        return user.pointer({
          keys: "[MouseRight]",
          target: screen.getByText("running"),
        });
      },
    ],
    // Radix opens on a long touch or pen press, with no contextmenu event.
    [
      "by a long press",
      () => {
        button("Elsewhere").focus();
        fireEvent.pointerDown(screen.getByText("running"), {
          pointerType: "touch",
        });
      },
    ],
    [
      "from the keyboard (Shift+F10, the Menu key)",
      () => {
        button("linux01").focus();
        fireEvent.contextMenu(button("linux01"));
      },
    ],
  ];

  it.each(openings)(
    "returns focus to the row a context menu was opened on %s",
    async (_how, open) => {
      const user = userEvent.setup();
      render(<FromContextMenu />);
      await open(user);
      await user.click(
        await screen.findByRole(
          "menuitem",
          { name: "Clone" },
          { timeout: 2000 },
        ),
      );
      expect(await screen.findByRole("dialog")).toBeInTheDocument();

      await user.keyboard("{Escape}");

      await waitFor(() => {
        expect(document.activeElement).toBe(button("linux01"));
      });
    },
  );
});

describe("dialogs opened from other dialogs", () => {
  // A secret shown right after the form that created it: the form's opener
  // is where both lead back to.
  function Chain() {
    const [form, setForm] = useState(false);
    const [secret, setSecret] = useState(false);
    return (
      <>
        <button onClick={set(setForm, true)}>Create token</button>
        <Dialog open={form} onOpenChange={setForm}>
          <DialogContent>
            <DialogTitle>Create token</DialogTitle>
            <DialogDescription>Name it.</DialogDescription>
            <button
              onClick={() => {
                setForm(false);
                setSecret(true);
              }}
            >
              Create
            </button>
          </DialogContent>
        </Dialog>
        <AlertDialog open={secret} onOpenChange={setSecret}>
          <AlertDialogContent>
            <AlertDialogTitle>Token created</AlertDialogTitle>
            <AlertDialogDescription>Copy it now.</AlertDialogDescription>
            <AlertDialogCancel>Done</AlertDialogCancel>
          </AlertDialogContent>
        </AlertDialog>
      </>
    );
  }

  it("returns focus to the first dialog's opener from one opened as it closed", async () => {
    const user = userEvent.setup();
    render(<Chain />);
    const opener = button("Create token");
    await user.click(opener);
    await user.click(button("Create"));
    const secret = await screen.findByRole("alertdialog");
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();

    await user.click(within(secret).getByRole("button", { name: "Done" }));

    await waitFor(() => {
      expect(document.activeElement).toBe(opener);
    });
  });

  // A confirmation that turns into another as it closes: a delete refused
  // for the credential Nexara signs in with, asking again with a warning.
  function ChainFromConfirmation() {
    const [confirm, setConfirm] = useState(false);
    const [warning, setWarning] = useState(false);
    return (
      <>
        <button onClick={set(setConfirm, true)}>Delete user</button>
        <AlertDialog open={confirm} onOpenChange={setConfirm}>
          <AlertDialogContent>
            <AlertDialogTitle>Delete the user?</AlertDialogTitle>
            <AlertDialogDescription>Its tokens go too.</AlertDialogDescription>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <button
              onClick={() => {
                setConfirm(false);
                setWarning(true);
              }}
            >
              Delete
            </button>
          </AlertDialogContent>
        </AlertDialog>
        <Dialog open={warning} onOpenChange={setWarning}>
          <DialogContent>
            <DialogTitle>This cuts off Nexara</DialogTitle>
            <DialogDescription>Type the name to go on.</DialogDescription>
          </DialogContent>
        </Dialog>
      </>
    );
  }

  it("returns focus to a confirmation's opener from a dialog opened as it closed", async () => {
    const user = userEvent.setup();
    render(<ChainFromConfirmation />);
    const opener = button("Delete user");
    await user.click(opener);
    await user.click(button("Delete"));
    expect(await screen.findByRole("dialog")).toBeInTheDocument();
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();

    await user.keyboard("{Escape}");

    await waitFor(() => {
      expect(document.activeElement).toBe(opener);
    });
  });

  // A confirmation inside a dialog that stays open, whose row goes once the
  // change is read back, like RoleAssignDialog's revoke: focus stays in the
  // dialog, the page behind it being hidden. The dialog re-renders when the
  // revoke settles, before the row goes, so the watch sees the row go first
  // (that re-creates the outer FocusScope's own MutationObserver after it).
  function RolesWithRevoke({
    held,
    note = "",
    revoked = null,
  }: {
    held: "disabled" | "not";
    note?: string;
    revoked?: string | null;
  }) {
    const [holding, setHolding] = useState<string | null>(null);
    const [pending, setPending] = useState<string | null>(null);
    const main = useRef<HTMLElement>(null);
    const roles = ["admin", "auditor"].filter((role) => role !== revoked);
    return (
      <FallbackFocusContext value={[() => main.current]}>
        <main ref={main} tabIndex={-1}>
          <Dialog open onOpenChange={() => undefined}>
            <DialogContent>
              <DialogTitle>Manage roles</DialogTitle>
              <DialogDescription>Who may do what.</DialogDescription>
              <p>{note}</p>
              {roles.map((role) => (
                <button
                  key={role}
                  disabled={holding === role}
                  onClick={() => {
                    setPending(role);
                  }}
                >
                  Revoke {role}
                </button>
              ))}
              <ConfirmDeleteDialog
                target={pending}
                onClose={() => {
                  setPending(null);
                }}
                onConfirm={(role) => {
                  if (held === "disabled") setHolding(role);
                }}
                title={(role) => `Revoke ${role}?`}
                description={() => "It stops at once."}
                confirmLabel="Revoke"
              />
            </DialogContent>
          </Dialog>
        </main>
      </FallbackFocusContext>
    );
  }

  it.each([
    { held: "disabled" as const, when: "held at close" },
    { held: "not" as const, when: "removed after focus went back to it" },
  ])(
    "keeps focus in the dialog a confirmation was opened from when its opener is $when",
    async ({ held }) => {
      const user = userEvent.setup();
      const { rerender } = render(<RolesWithRevoke held={held} />);
      const roles = screen.getByRole("dialog", { name: "Manage roles" });
      const revoke = button("Revoke admin");
      await user.click(revoke);
      const confirm = await screen.findByRole("alertdialog");
      await user.click(within(confirm).getByRole("button", { name: "Revoke" }));
      await waitFor(() => {
        expect(document.activeElement).toBe(
          held === "disabled" ? roles : revoke,
        );
      });

      rerender(<RolesWithRevoke held={held} note="Revoked admin." />);
      rerender(
        <RolesWithRevoke held={held} note="Revoked admin." revoked="admin" />,
      );
      await timersRun();

      expect(document.activeElement).toBe(roles);
    },
  );

  // Inside the page's main content, as in the app; `closeOuter` closes the
  // outer dialog from the app, with no key or pointer.
  function Nested({ closeOuter = false }: { closeOuter?: boolean }) {
    const [outer, setOuter] = useState(false);
    const [inner, setInner] = useState(false);
    const main = useRef<HTMLElement>(null);
    useEffect(() => {
      if (closeOuter) setOuter(false);
    }, [closeOuter]);
    return (
      <FallbackFocusContext value={[() => main.current]}>
        <main ref={main} tabIndex={-1}>
          <button onClick={set(setOuter, true)}>Roles</button>
        </main>
        <Dialog open={outer} onOpenChange={setOuter}>
          <DialogContent>
            <DialogTitle>Roles</DialogTitle>
            <DialogDescription>Who may do what.</DialogDescription>
            <button onClick={set(setInner, true)}>Revoke admin</button>
            <AlertDialog open={inner} onOpenChange={setInner}>
              <AlertDialogContent>
                <AlertDialogTitle>Revoke admin?</AlertDialogTitle>
                <AlertDialogDescription>
                  It stops at once.
                </AlertDialogDescription>
                <AlertDialogCancel>Cancel</AlertDialogCancel>
              </AlertDialogContent>
            </AlertDialog>
          </DialogContent>
        </Dialog>
      </FallbackFocusContext>
    );
  }

  async function backOnRevokeInRoles(user: User) {
    await user.click(button("Roles"));
    const revoke = button("Revoke admin");
    await user.click(revoke);
    const confirm = await screen.findByRole("alertdialog");
    await user.click(within(confirm).getByRole("button", { name: "Cancel" }));
    await waitFor(() => {
      expect(document.activeElement).toBe(revoke);
    });
    expect(screen.getByRole("dialog")).toBeInTheDocument();
  }

  // The watch on Revoke admin must not take over when the outer dialog goes
  // and takes that button with it: the outer dialog puts focus back itself.
  it("returns focus into the dialog a nested one was opened from, then out of it", async () => {
    const user = userEvent.setup();
    render(<Nested />);
    const opener = button("Roles");
    await backOnRevokeInRoles(user);

    await user.keyboard("{Escape}");

    await waitFor(() => {
      expect(document.activeElement).toBe(opener);
    });
    await timersRun();
    expect(document.activeElement).toBe(opener);
  });

  it("returns focus out of the dialog a nested one was opened from when the app closes it", async () => {
    const user = userEvent.setup();
    const { rerender } = render(<Nested />);
    const opener = button("Roles");
    await backOnRevokeInRoles(user);

    rerender(<Nested closeOuter />);

    await waitFor(() => {
      expect(document.activeElement).toBe(opener);
    });
    await timersRun();
    expect(document.activeElement).toBe(opener);
  });

  // Opened from inside a modal that stays open, but rendered beside it:
  // VMContextDialogs, opened from the mobile nav Sheet's tree. The page
  // behind the Sheet is hidden, so focus stays in the Sheet.
  function SiblingOverSheet({
    openerShown = true,
    removeOnDestroy = false,
  }: {
    openerShown?: boolean;
    removeOnDestroy?: boolean;
  }) {
    const [confirm, setConfirm] = useState(false);
    const [destroyed, setDestroyed] = useState(false);
    const main = useRef<HTMLElement>(null);
    const shown = openerShown && !(removeOnDestroy && destroyed);
    return (
      <FallbackFocusContext value={[() => main.current]}>
        <main ref={main} tabIndex={-1}>
          Page
        </main>
        <Sheet open onOpenChange={() => undefined}>
          <SheetContent>
            <SheetTitle>Navigation</SheetTitle>
            <SheetDescription>Pages and resources.</SheetDescription>
            {shown && (
              <button onClick={set(setConfirm, true)}>Destroy linux01</button>
            )}
          </SheetContent>
        </Sheet>
        <Dialog open={confirm} onOpenChange={setConfirm}>
          <DialogContent>
            <DialogTitle>Destroy linux01?</DialogTitle>
            <DialogDescription>It is removed for good.</DialogDescription>
            <button
              onClick={() => {
                setDestroyed(true);
                setConfirm(false);
              }}
            >
              Destroy
            </button>
          </DialogContent>
        </Dialog>
      </FallbackFocusContext>
    );
  }

  it("keeps focus in a modal still open when a dialog opened over it loses its opener at close", async () => {
    const user = userEvent.setup();
    render(<SiblingOverSheet removeOnDestroy />);
    await user.click(button("Destroy linux01"));

    await user.click(button("Destroy"));

    await waitFor(() => {
      expect(document.activeElement).toBe(
        screen.getByRole("dialog", { name: "Navigation" }),
      );
    });
  });

  // Two modals open, one over the other, and a third opened from the top
  // one but rendered beside it: back into the top one, which hides the
  // other.
  function StackedOverSheet() {
    const [details, setDetails] = useState(false);
    const [confirm, setConfirm] = useState(false);
    const [destroyed, setDestroyed] = useState(false);
    const main = useRef<HTMLElement>(null);
    return (
      <FallbackFocusContext value={[() => main.current]}>
        <main ref={main} tabIndex={-1}>
          Page
        </main>
        <Sheet open onOpenChange={() => undefined}>
          <SheetContent>
            <SheetTitle>Navigation</SheetTitle>
            <SheetDescription>Pages and resources.</SheetDescription>
            <button onClick={set(setDetails, true)}>linux01</button>
          </SheetContent>
        </Sheet>
        <Dialog open={details} onOpenChange={setDetails}>
          <DialogContent>
            <DialogTitle>linux01</DialogTitle>
            <DialogDescription>Running.</DialogDescription>
            {!destroyed && (
              <button onClick={set(setConfirm, true)}>Destroy linux01</button>
            )}
          </DialogContent>
        </Dialog>
        <AlertDialog open={confirm} onOpenChange={setConfirm}>
          <AlertDialogContent>
            <AlertDialogTitle>Destroy linux01?</AlertDialogTitle>
            <AlertDialogDescription>
              It is removed for good.
            </AlertDialogDescription>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <button
              onClick={() => {
                setDestroyed(true);
                setConfirm(false);
              }}
            >
              Destroy
            </button>
          </AlertDialogContent>
        </AlertDialog>
      </FallbackFocusContext>
    );
  }

  it("falls back to the topmost of the modals still open", async () => {
    const user = userEvent.setup();
    render(<StackedOverSheet />);
    await user.click(button("linux01"));
    await user.click(button("Destroy linux01"));
    const confirm = await screen.findByRole("alertdialog");

    await user.click(within(confirm).getByRole("button", { name: "Destroy" }));

    await waitFor(() => {
      expect(document.activeElement).toBe(
        screen.getByRole("dialog", { name: "linux01" }),
      );
    });
  });

  it("keeps focus in a modal still open when a dialog opened over it loses its opener later", async () => {
    const user = userEvent.setup();
    const { rerender } = render(<SiblingOverSheet />);
    const opener = button("Destroy linux01");
    await user.click(opener);
    await user.keyboard("{Escape}");
    await waitFor(() => {
      expect(document.activeElement).toBe(opener);
    });

    rerender(<SiblingOverSheet openerShown={false} />);

    await waitFor(() => {
      expect(document.activeElement).toBe(
        screen.getByRole("dialog", { name: "Navigation" }),
      );
    });
  });

  // A dialog that hides nothing (non-modal) keeps the page reachable, so it
  // takes focus back ahead of the page because it was opened from, not
  // because the page is hidden.
  function NestedInNonModal() {
    const [pending, setPending] = useState(false);
    const [gone, setGone] = useState(false);
    const main = useRef<HTMLElement>(null);
    return (
      <FallbackFocusContext value={[() => main.current]}>
        <main ref={main} tabIndex={-1}>
          Page
        </main>
        <Dialog modal={false} open onOpenChange={() => undefined}>
          <DialogContent>
            <DialogTitle>Details</DialogTitle>
            <DialogDescription>Read only.</DialogDescription>
            {!gone && (
              <button onClick={set(setPending, true)}>Remove tag</button>
            )}
            <AlertDialog open={pending} onOpenChange={setPending}>
              <AlertDialogContent>
                <AlertDialogTitle>Remove the tag?</AlertDialogTitle>
                <AlertDialogDescription>
                  It goes at once.
                </AlertDialogDescription>
                <AlertDialogCancel>Cancel</AlertDialogCancel>
                <button
                  onClick={() => {
                    setGone(true);
                    setPending(false);
                  }}
                >
                  Remove
                </button>
              </AlertDialogContent>
            </AlertDialog>
          </DialogContent>
        </Dialog>
      </FallbackFocusContext>
    );
  }

  it("returns focus to the dialog a confirmation was opened from ahead of the page around it", async () => {
    const user = userEvent.setup();
    render(<NestedInNonModal />);
    await user.click(button("Remove tag"));

    await user.click(button("Remove"));

    await waitFor(() => {
      expect(document.activeElement).toBe(
        screen.getByRole("dialog", { name: "Details" }),
      );
    });
  });
});

describe("a dialog with a Trigger", () => {
  function WithTrigger({ removeOnSave = false }: { removeOnSave?: boolean }) {
    const [shown, setShown] = useState(true);
    const card = useRef<HTMLDivElement>(null);
    return (
      <>
        <div ref={card} role="region" aria-label="Card" tabIndex={-1}>
          Card
        </div>
        <Dialog>
          {shown && (
            <DialogTrigger asChild>
              <button>Edit</button>
            </DialogTrigger>
          )}
          <DialogContent fallbackFocus={() => card.current}>
            <DialogTitle>Edit linux01</DialogTitle>
            <DialogDescription>Change its settings.</DialogDescription>
            <DialogClose asChild>
              <button
                onClick={() => {
                  if (removeOnSave) setShown(false);
                }}
              >
                Save
              </button>
            </DialogClose>
          </DialogContent>
        </Dialog>
      </>
    );
  }

  it("still returns focus to its Trigger", async () => {
    const user = userEvent.setup();
    render(<WithTrigger />);
    const trigger = button("Edit");
    await user.click(trigger);

    await user.keyboard("{Escape}");

    await waitFor(() => {
      expect(document.activeElement).toBe(trigger);
    });
  });

  it("puts focus on its fallback when its Trigger is gone", async () => {
    const user = userEvent.setup();
    render(<WithTrigger removeOnSave />);
    await user.click(button("Edit"));

    await user.click(button("Save"));

    await waitFor(() => {
      expect(document.activeElement).toBe(region("Card"));
    });
  });
});

describe("a ref on the content", () => {
  it("keeps the cleanup a callback ref returns", () => {
    const seen: (HTMLDivElement | null)[] = [];
    const cleanup = vi.fn();
    const { unmount } = render(
      <Dialog open>
        <DialogContent
          ref={(node) => {
            seen.push(node);
            return cleanup;
          }}
        >
          <DialogTitle>Edit linux01</DialogTitle>
          <DialogDescription>Change its settings.</DialogDescription>
        </DialogContent>
      </Dialog>,
    );
    expect(seen).toHaveLength(1);

    unmount();

    expect(cleanup).toHaveBeenCalledTimes(1);
    expect(seen).toHaveLength(1);
  });
});

describe("resolveOpener", () => {
  it("has no opener for <body> or nothing", () => {
    expect(resolveOpener(document.body)).toBeNull();
    expect(resolveOpener(null)).toBeNull();
  });

  it("has no opener for an element that is not HTML", () => {
    const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
    document.body.append(svg);
    expect(resolveOpener(svg)).toBeNull();
    svg.remove();
  });

  it("keeps an element outside any menu or closing dialog", () => {
    const { getByRole } = render(<button>Edit</button>);
    expect(resolveOpener(getByRole("button"))).toBe(getByRole("button"));
  });

  it("gives up on a menu labelled by its own item instead of looping", () => {
    const { getByRole } = render(
      <div role="menu" aria-labelledby="loop">
        <button id="loop">Edit</button>
      </div>,
    );
    expect(resolveOpener(getByRole("button", { hidden: true }))).toBeNull();
  });
});
