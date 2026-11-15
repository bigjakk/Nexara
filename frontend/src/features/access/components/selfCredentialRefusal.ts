import { ApiClientError } from "@/lib/api-client";

/**
 * Whether the server refused an action because it can cut Nexara off from the
 * cluster, or take away permissions it relies on: a 409 from the self-credential
 * guards (handlers/access.go, handlers/access_dependency.go), which says why and
 * how to go ahead anyway (force=true). Every flow they can refuse answers it
 * with a type-to-confirm override (SelfCredentialConfirm), which sends the
 * action again with force, and decides to open that override with this.
 */
export function isOverridable(err: unknown): err is ApiClientError {
  return err instanceof ApiClientError && err.status === 409;
}

/**
 * What a refusal that has an override says when there is no one left to give
 * it: the dialog is gone. The server's words end with "Retry with force=true to
 * proceed anyway", which is for an API caller and cannot be acted on from a
 * toast, so they are not used: this says what happened to the action, and what
 * to do to go ahead.
 */
export function refusedBeyondOverride(action: string): string {
  return `${action} was refused, and nothing was changed, because it could cut Nexara off from the cluster. To go ahead anyway, do it again and confirm the override that is then offered.`;
}
