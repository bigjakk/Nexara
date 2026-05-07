import { ApiClientError } from "@/lib/api-client";

export interface PrivateAddressWarning {
  ip: string;
}

/**
 * Returns the resolved private-address details if `err` is a 422 from the
 * SSRF policy gate, otherwise null. Callers use this to swap a generic error
 * banner for a "this resolves to a private IP — confirm to proceed" prompt.
 */
export function privateAddressWarningFromError(
  err: unknown,
): PrivateAddressWarning | null {
  if (
    !(err instanceof ApiClientError) ||
    err.status !== 422 ||
    err.body.error !== "private_address_confirm_required"
  ) {
    return null;
  }
  const details = err.body.details;
  let ip = "";
  if (details != null && typeof details === "object" && "ip" in details) {
    const raw = (details as { ip?: unknown }).ip;
    if (typeof raw === "string") {
      ip = raw;
    }
  }
  return { ip };
}
