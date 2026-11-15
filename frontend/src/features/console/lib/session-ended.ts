import { confirmSession } from "@/lib/api-client";

/**
 * What the server says of a console whose sign-in has ended — signed out,
 * revoked, the account disabled (internal/ws/console_credential.go): an error
 * with this code when it is refused at open, a Close with this code while it
 * is open. Reconnecting would only be refused again, so the console stops.
 */
export const SESSION_ENDED_CODE = "session_ended";
export const SESSION_ENDED_CLOSE = 3000;

/**
 * The Close the server sends a console whose user no longer holds the
 * permission to it (a grant revoked, a role changed). Final too, but the
 * sign-in is untouched: nothing here may sign the user out.
 */
export const ACCESS_REMOVED_CLOSE = 3003;

/**
 * Asks the app's session to check itself: its access token outlives the
 * session by up to its own lifetime, and the refresh this makes is refused,
 * which ends the session in the app now rather than when the token runs out.
 */
export function consoleSessionEnded(): void {
  void confirmSession();
}
