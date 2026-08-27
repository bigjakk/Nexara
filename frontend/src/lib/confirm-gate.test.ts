import { describe, it, expect } from "vitest";
import { confirmRequiredFromError, confirmDetailString } from "./confirm-gate";
import { ApiClientError } from "@/lib/api-client";

function gate422(code: string, details?: Record<string, unknown>) {
  return new ApiClientError(422, {
    error: code,
    message: "Confirm to proceed.",
    // exactOptionalPropertyTypes: omit the key entirely rather than setting it
    // to undefined, which is what a real response without details looks like.
    ...(details !== undefined ? { details } : {}),
  });
}

describe("confirmRequiredFromError", () => {
  it("recognises a gate whose code the caller handles", () => {
    const confirm = confirmRequiredFromError(
      gate422("insecure_ldap_transport_confirm_required", {
        transport_kind: "cleartext",
      }),
      ["insecure_ldap_transport_confirm_required"],
      "cfg-1",
    );

    if (confirm === null) throw new Error("expected the gate to be recognised");
    expect(confirm.code).toBe("insecure_ldap_transport_confirm_required");
    expect(confirm.target).toBe("cfg-1");
    expect(confirmDetailString(confirm, "transport_kind")).toBe("cleartext");
  });

  // A page must not render a confirm button for a gate it cannot satisfy —
  // the acknowledgement field it would resend is a different one, so the
  // request would be refused again forever. Unknown gates fall through to the
  // generic error banner instead.
  it("ignores a 422 whose code the caller does not handle", () => {
    expect(
      confirmRequiredFromError(
        gate422("some_other_confirm_required"),
        ["insecure_ldap_transport_confirm_required"],
        null,
      ),
    ).toBeNull();
  });

  it("ignores non-422 errors and non-API errors", () => {
    expect(
      confirmRequiredFromError(
        new ApiClientError(400, { error: "bad", message: "no" }),
        ["bad"],
        null,
      ),
    ).toBeNull();
    expect(
      confirmRequiredFromError(new Error("network down"), ["bad"], null),
    ).toBeNull();
  });

  it("tolerates a missing or non-object details blob", () => {
    const confirm = confirmRequiredFromError(gate422("x", undefined), ["x"], null);
    if (confirm === null) throw new Error("expected the gate to be recognised");
    expect(confirmDetailString(confirm, "anything")).toBeNull();
  });

  // The target is what stops a mutation that settles late from leaving its
  // prompt on a config the operator has since switched to — the confirm button
  // would otherwise submit THAT config with an acknowledgement given for
  // another one.
  it("carries the target it was given so callers can match it", () => {
    expect(confirmRequiredFromError(gate422("x"), ["x"], "cfg-a")?.target).toBe("cfg-a");
    expect(confirmRequiredFromError(gate422("x"), ["x"], null)?.target).toBeNull();
  });
});
