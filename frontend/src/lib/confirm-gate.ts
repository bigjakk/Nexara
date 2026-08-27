import { ApiClientError } from "@/lib/api-client";

/**
 * A refusal the caller can still choose to override, by re-submitting with an
 * acknowledgement flag. The backend returns these as a 422 carrying a stable
 * `error` code (see internal/api/handlers/confirm_gate.go); the UI turns them
 * into a prompt instead of an unexplained failure.
 */
export interface ConfirmRequired {
  /** The gate's stable code, so a page handling several can tell them apart. */
  code: string;
  message: string;
  details: Record<string, unknown>;
  /**
   * Which config the refusal belongs to — the id being edited, or null for a
   * create. A mutation that settles AFTER the operator has switched configs
   * would otherwise leave the prompt mounted on a different one, and its
   * confirm button would submit that config with an acknowledgement given for
   * another. Callers compare this against what is currently open.
   */
  target: string | null;
}

/**
 * Returns the confirm-required details if `err` is one of the backend's 422
 * confirm gates and its code is in `codes`, otherwise null.
 *
 * Pass the codes the caller actually knows how to prompt for: an unrecognised
 * gate must fall through to the generic error banner rather than render a
 * confirm button the page cannot honour.
 */
export function confirmRequiredFromError(
  err: unknown,
  codes: readonly string[],
  target: string | null,
): ConfirmRequired | null {
  if (!(err instanceof ApiClientError) || err.status !== 422) {
    return null;
  }
  const code = err.body.error;
  if (typeof code !== "string" || !codes.includes(code)) {
    return null;
  }

  const raw = err.body.details;
  const details = raw != null && typeof raw === "object" ? raw : {};

  return {
    code,
    message: err.body.message,
    details,
    target,
  };
}

/** Reads a string field out of a gate's details blob. */
export function confirmDetailString(
  confirm: ConfirmRequired,
  key: string,
): string | null {
  const value = confirm.details[key];
  return typeof value === "string" ? value : null;
}
