import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { HardDrive } from "lucide-react";
import { Input } from "@/components/ui/input";
import { Check, DeviceList, DeviceRow, Field, SaveBar } from "./HardwareRows";
import { RowsReadOnlyContext } from "./read-only";

// A role that may not change the guest's config was offered every control on
// the Hardware and Resources tabs, each answering 403.
describe.each([true, false])("the row kit, read-only %s", (readOnly) => {
  it("draws its controls for that", () => {
    render(
      <RowsReadOnlyContext value={readOnly}>
        <Field label="Cores">
          <Input defaultValue="2" />
        </Field>
        <Check label="NUMA" />
        <DeviceList>
          <DeviceRow
            name="scsi0"
            icon={HardDrive}
            title="Hard disk"
            actions={<button type="button">Remove</button>}
            ownActions={<button type="button">Move volume</button>}
            editor={<input aria-label="Cache" />}
            open
            onToggle={() => undefined}
          />
          <DeviceRow
            name="rootfs"
            icon={HardDrive}
            title="Root disk"
            editor={<input aria-label="Resize to" />}
            onToggle={() => undefined}
            editLabel="Resize rootfs"
          />
        </DeviceList>
        <SaveBar
          dirty={false}
          saving={false}
          disabled={false}
          onSave={() => undefined}
        />
      </RowsReadOnlyContext>,
    );

    // toBeDisabled follows a disabled fieldset, which el.disabled does not.
    const disabled = readOnly ? "toBeDisabled" : "toBeEnabled";
    expect(screen.getByRole("textbox", { name: "Cores" }))[disabled]();
    expect(screen.getByRole("checkbox", { name: "NUMA" }))[disabled]();
    // A device's own fields still open, to be read.
    expect(
      screen.getByRole("button", {
        name: readOnly ? "Show scsi0" : "Edit scsi0",
      }),
    ).toBeEnabled();
    expect(screen.getByRole("textbox", { name: "Cache" }))[disabled]();
    // An editor that does one thing, a resize, is not opened read-only.
    expect(screen.queryByRole("button", { name: "Show rootfs" })).toBeNull();
    // A control with a permission of its own stays either way.
    expect(screen.getByRole("button", { name: "Move volume" })).toBeEnabled();
    for (const name of ["Remove", "Resize rootfs", "Save changes"]) {
      expect(screen.queryByRole("button", { name }) === null).toBe(readOnly);
    }
  });
});
