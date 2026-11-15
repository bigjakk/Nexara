import { describe, expect, it } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { GuestStateFact } from "./GuestStateFact";

describe("GuestStateFact", () => {
  it("names a halted VM's state and what to do in the guest page header", () => {
    renderWithProviders(
      <GuestStateFact status="running" runState="io-error" uptime={600} />,
    );
    expect(screen.getByText("I/O error")).toBeInTheDocument();
    expect(
      screen.getByText(/Resume once the storage is back/),
    ).toBeInTheDocument();
    expect(screen.queryByText("Running")).not.toBeInTheDocument();
  });
});
