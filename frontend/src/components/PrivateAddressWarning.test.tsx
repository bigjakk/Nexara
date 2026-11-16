import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { PrivateAddressWarning } from "./PrivateAddressWarning";

describe("PrivateAddressWarning", () => {
  // It said "a private/loopback IP" of every address, loopback or not.
  it.each([
    { ip: "10.0.0.5", kind: "an address on a private network" },
    { ip: "127.0.0.1", kind: "a loopback address" },
    { ip: "::1", kind: "a loopback address" },
    { ip: "169.254.10.1", kind: "a link-local address" },
    { ip: "fe80::1", kind: "a link-local address" },
  ])("names $ip $kind", ({ ip, kind }) => {
    render(<PrivateAddressWarning ip={ip} onConfirm={() => undefined} />);
    expect(screen.getByText(/resolves to/)).toHaveTextContent(
      `That URL resolves to ${kind} (${ip}).`,
    );
  });
});
