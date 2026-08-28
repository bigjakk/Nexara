import { describe, it, expect, beforeEach, afterEach } from "vitest";
import { render, screen } from "@testing-library/react";
import { ChevronsUpDown, Loader2, Power } from "lucide-react";
import { Button } from "./button";
import {
  usePreferencesStore,
  type ButtonDisplay,
} from "@/stores/preferences-store";

function setMode(buttonDisplay: ButtonDisplay) {
  usePreferencesStore.setState((s) => ({
    preferences: { ...s.preferences, buttonDisplay },
  }));
}

describe("Button — display mode", () => {
  beforeEach(() => {
    setMode("icon-text");
  });
  afterEach(() => {
    // setMode goes through setState, which never touches localStorage — the
    // store is what leaks between tests, so that is what has to be reset.
    setMode("icon-text");
  });

  it("renders both the icon and the label by default", () => {
    render(
      <Button>
        <Power className="h-4 w-4" />
        Shutdown
      </Button>,
    );
    const btn = screen.getByRole("button", { name: /shutdown/i });
    expect(btn).toHaveTextContent("Shutdown");
    expect(btn.querySelector("svg")).not.toBeNull();
  });

  it("icon mode drops the label but keeps it as the accessible name and tooltip", () => {
    setMode("icon");
    render(
      <Button>
        <Power className="h-4 w-4" />
        Shutdown
      </Button>,
    );
    const btn = screen.getByRole("button", { name: "Shutdown" });
    expect(btn).not.toHaveTextContent("Shutdown");
    expect(btn.querySelector("svg")).not.toBeNull();
    expect(btn).toHaveAttribute("title", "Shutdown");
  });

  it("text mode drops the icon and keeps the label", () => {
    setMode("text");
    render(
      <Button>
        <Power className="h-4 w-4" />
        Shutdown
      </Button>,
    );
    const btn = screen.getByRole("button", { name: /shutdown/i });
    expect(btn).toHaveTextContent("Shutdown");
    expect(btn.querySelector("svg")).toBeNull();
  });

  it("leaves an icon-only button alone in text mode", () => {
    setMode("text");
    render(
      <Button size="icon" aria-label="Close">
        <Power className="h-4 w-4" />
      </Button>,
    );
    // Stripping the glyph here would leave an empty, unclickable-looking button.
    expect(screen.getByRole("button", { name: "Close" }).querySelector("svg")).not.toBeNull();
  });

  it("leaves a text-only button alone in icon mode", () => {
    setMode("icon");
    render(<Button>Cancel</Button>);
    // Confirm dialogs are text-only, so they keep reading as words in every mode.
    expect(screen.getByRole("button", { name: "Cancel" })).toHaveTextContent("Cancel");
  });

  it("treats a wrapping element as label content, not as an icon", () => {
    setMode("text");
    render(
      <Button>
        <Power className="h-4 w-4" />
        <span>Shutdown</span>
      </Button>,
    );
    const btn = screen.getByRole("button", { name: /shutdown/i });
    expect(btn).toHaveTextContent("Shutdown");
    expect(btn.querySelector("svg")).toBeNull();
  });

  it("keeps the glyph when the only label is an sr-only span", () => {
    setMode("text");
    render(
      <Button size="icon">
        <Power className="h-4 w-4" />
        <span className="sr-only">Toggle theme</span>
      </Button>,
    );
    // The Shadcn icon-button pattern: treating the hidden label as visible text
    // would strip the glyph and leave a blank button (the theme toggle bug).
    const btn = screen.getByRole("button", { name: "Toggle theme" });
    expect(btn.querySelector("svg")).not.toBeNull();
  });

  it("uses only the visible label as the tooltip, ignoring sr-only text", () => {
    setMode("icon");
    render(
      <Button>
        <Power className="h-4 w-4" />
        Shutdown
        <span className="sr-only">the selected guest</span>
      </Button>,
    );
    expect(screen.getByRole("button", { name: "Shutdown" })).toHaveAttribute(
      "title",
      "Shutdown",
    );
  });


  it("leaves a trailing glyph alone — it annotates the label, not replaces it", () => {
    setMode("icon");
    render(
      <Button variant="ghost" size="sm">
        Name
        <ChevronsUpDown className="ml-1 h-3 w-3" />
      </Button>,
    );
    // Sort headers and dropdown triggers put the glyph last. Collapsing them
    // would leave a row of identical chevrons where the column names were.
    expect(screen.getByRole("button", { name: /name/i })).toHaveTextContent("Name");
  });

  it("leaves a combobox trigger alone — its text is the selected value", () => {
    setMode("icon");
    render(
      <Button role="combobox">
        <span>3 guests selected</span>
        <ChevronsUpDown className="h-4 w-4" />
      </Button>,
    );
    expect(screen.getByRole("combobox")).toHaveTextContent("3 guests selected");
  });

  it("text mode never strips an in-flight spinner", () => {
    setMode("text");
    render(
      <Button disabled>
        <Loader2 className="mr-2 h-4 w-4 animate-spin" />
        Signing in
      </Button>,
    );
    // In text mode the glyph is the only progress signal the button has left.
    const btn = screen.getByRole("button", { name: /signing in/i });
    expect(btn.querySelector("svg")).not.toBeNull();
    expect(btn).toHaveTextContent("Signing in");
  });

  it("icon mode keeps a pending button the same width as its idle self", () => {
    setMode("icon");
    const Save = (p: { className?: string }) => <svg {...p} />;
    const { rerender } = render(
      <Button>
        <Save className="h-4 w-4" />
        Save
      </Button>,
    );
    const idle = screen.getByRole("button").innerHTML;
    rerender(
      <Button>
        <Loader2 className="h-4 w-4 animate-spin" />
        Save
      </Button>,
    );
    const btn = screen.getByRole("button", { name: "Save" });
    // Both states collapse to a single glyph, so the button does not jump
    // width when an action starts; the spinner is still visible.
    expect(btn).not.toHaveTextContent("Save");
    expect(btn.querySelector("svg")).not.toBeNull();
    expect(idle.includes("Save")).toBe(false);
  });

  it("reshapes a leading glyph even when a dropdown chevron trails the label", () => {
    setMode("icon");
    render(
      <Button variant="ghost" size="sm">
        <Power className="h-3.5 w-3.5" />
        Power
        <ChevronsUpDown className="h-3 w-3" />
      </Button>,
    );
    // The console toolbar shape: a real leading-icon action button that also
    // carries a dropdown affordance. It collapses to its identity glyph.
    const btn = screen.getByRole("button", { name: "Power" });
    expect(btn).not.toHaveTextContent("Power");
    expect(btn.querySelectorAll("svg")).toHaveLength(1);
    // The identity glyph survives, not the chevron — an inverted rule would
    // still leave exactly one svg here, so pin which one it is.
    expect(btn.querySelector("svg")?.getAttribute("class")).toContain("lucide-power");
  });

  it("text mode keeps a trailing affordance while dropping the identity glyph", () => {
    setMode("text");
    render(
      <Button variant="ghost" size="sm">
        <Power className="h-3.5 w-3.5" />
        Power
        <ChevronsUpDown className="h-3 w-3" />
      </Button>,
    );
    const btn = screen.getByRole("button", { name: /power/i });
    expect(btn).toHaveTextContent("Power");
    // The chevron still signals that this opens a menu — and it is the
    // chevron that survives here, not the identity glyph.
    expect(btn.querySelectorAll("svg")).toHaveLength(1);
    expect(btn.querySelector("svg")?.getAttribute("class")).toContain(
      "lucide-chevrons-up-down",
    );
  });

  it("reshapes through nested fragments", () => {
    setMode("text");
    render(
      <Button>
        <>
          <>
            <Power className="h-4 w-4" />
            Shutdown
          </>
        </>
      </Button>,
    );
    const btn = screen.getByRole("button", { name: /shutdown/i });
    expect(btn).toHaveTextContent("Shutdown");
    expect(btn.querySelector("svg")).toBeNull();
  });

  it("reshapes children wrapped in a fragment", () => {
    setMode("text");
    render(
      <Button>
        <>
          <Power className="h-4 w-4" />
          Shutdown
        </>
      </Button>,
    );
    const btn = screen.getByRole("button", { name: /shutdown/i });
    expect(btn).toHaveTextContent("Shutdown");
    expect(btn.querySelector("svg")).toBeNull();
  });

  it("keeps the glyph when the label is hidden at small breakpoints", () => {
    setMode("text");
    render(
      <Button>
        <Power className="h-4 w-4" />
        <span className="hidden sm:inline">Create</span>
      </Button>,
    );
    // Below `sm` the span is display:none, so dropping the glyph too would
    // leave a button with nothing in it at all.
    expect(screen.getByRole("button").querySelector("svg")).not.toBeNull();
  });

  it("ignores an unrecognised stored mode instead of stripping icons", () => {
    usePreferencesStore.setState((s) => ({
      preferences: { ...s.preferences, buttonDisplay: "compact" as ButtonDisplay },
    }));
    render(
      <Button>
        <Power className="h-4 w-4" />
        Shutdown
      </Button>,
    );
    const btn = screen.getByRole("button", { name: /shutdown/i });
    expect(btn).toHaveTextContent("Shutdown");
    expect(btn.querySelector("svg")).not.toBeNull();
  });

  it("icon-mode padding overrides both the size variant and a caller class", () => {
    setMode("icon");
    render(
      <Button size="sm" className="px-6">
        <Power className="h-4 w-4" />
        Shutdown
      </Button>,
    );
    const btn = screen.getByRole("button", { name: "Shutdown" });
    expect(btn.className).toContain("px-2");
    expect(btn.className).not.toContain("px-6");
    expect(btn.className).not.toContain("px-3");
  });

  it("lets a disabled icon-only button still show its tooltip", () => {
    setMode("icon");
    render(
      <Button disabled>
        <Power className="h-4 w-4" />
        Destroy
      </Button>,
    );
    // disabled:pointer-events-none would suppress the native title tooltip,
    // leaving an unidentifiable glyph.
    const btn = screen.getByRole("button", { name: "Destroy" });
    expect(btn.className).toContain("disabled:pointer-events-auto");
    // The override only wins because tailwind-merge removes the conflicting
    // class — asserting the presence of "auto" alone would not catch that.
    expect(btn.className).not.toContain("disabled:pointer-events-none");
    // Restoring hit-testing must not make a disabled button paint as hoverable:
    // every variant scopes its hover under not-disabled: so it cannot match.
    expect(btn.className).toContain("not-disabled:hover:");
    expect(btn.className).not.toMatch(/(^|\s)hover:/);
  });

  it("does not reshape an asChild button", () => {
    setMode("icon");
    render(
      <Button asChild>
        <a href="/somewhere">
          <Power className="h-4 w-4" />
          Shutdown
        </a>
      </Button>,
    );
    // Slot requires a single child element — rewriting children would break it.
    expect(screen.getByRole("link", { name: /shutdown/i })).toHaveTextContent("Shutdown");
  });

  it("keeps a caller-supplied aria-label and title", () => {
    setMode("icon");
    render(
      <Button aria-label="Power off guest" title="Custom tip">
        <Power className="h-4 w-4" />
        Shutdown
      </Button>,
    );
    const btn = screen.getByRole("button", { name: "Power off guest" });
    expect(btn).toHaveAttribute("title", "Custom tip");
  });
});
