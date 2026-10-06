import { expect } from "vitest";
import { fireEvent } from "@testing-library/react";
import userEvent, { type UserEvent } from "@testing-library/user-event";

/**
 * A user-event that does not wait between the steps of an action. Its default
 * (delay: 0) lets a timer run after every pointer step, about 10 ms a click on
 * a loaded box; what a test needs React or TanStack to settle it awaits with
 * findBy* or waitFor, not with the pace of its own input.
 */
export function setupUser(): UserEvent {
  return userEvent.setup({ delay: null });
}

/**
 * Sets a field, or picks a select's option, in one change event: what typing or
 * choosing comes to, for a test that is not about the keystrokes (a big panel
 * renders whole on each one). It refuses what user.type and user.selectOptions
 * refuse, so that a control a user could not use does not pass for one they
 * could: a field that is disabled or read only, and an option that is missing
 * from its select or disabled.
 */
export function fill(field: HTMLElement, value: string): void {
  expect(field).toBeEnabled();
  expect(field).not.toHaveAttribute("readonly");
  if (field instanceof HTMLSelectElement) {
    const option = Array.from(field.options).find((o) => o.value === value);
    if (!option) throw new Error(`the select has no option "${value}"`);
    expect(option).toBeEnabled();
  }
  fireEvent.change(field, { target: { value } });
}
