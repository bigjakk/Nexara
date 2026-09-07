import { ApiClientError } from "@/lib/api-client";

/**
 * What to show an operator about a failed request.
 *
 * For an API failure that is the server's own message, or a bare status when
 * the body carried none — a proxy-generated 502 has no JSON body and, over
 * HTTP/2, no statusText.
 *
 * Anything else that reaches a query as an Error was thrown by our own client
 * — `unwrapList`'s envelope check is the one that matters, and its message
 * naming the offending path is the entire reason it throws — so it is worth
 * quoting too. The exception is a TypeError, which is how a dropped
 * connection rejects `fetch`: its message is the browser's own wording and
 * names nothing the surrounding copy does not already say.
 */
export function describeError(error: unknown): string {
  if (error instanceof ApiClientError) {
    return error.message !== ""
      ? error.message
      : `HTTP ${String(error.status)}`;
  }
  if (error instanceof Error && !(error instanceof TypeError)) {
    return error.message;
  }
  return "";
}
