import type { ReactNode } from "react";
import { AlertTriangle } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { describeError } from "@/lib/api-error";
import {
  retryState,
  useSettledQueryError,
  type QueryStateLike,
} from "@/hooks/useSettledQueryError";

/** Why a retry is not on offer. Named once; both notices say it. */
const PAUSED_RETRY =
  "The retry is paused. It resumes on its own once this tab is in the foreground and the browser is online.";

interface RetryButtonProps {
  query: QueryStateLike;
  className?: string;
}

/** Renders nothing when a click could not start a fetch. See retryState. */
function RetryButton({ query, className = "" }: RetryButtonProps) {
  const { offer, busy } = retryState(query);
  if (!offer) return null;
  return (
    <Button
      variant="outline"
      size="sm"
      className={`h-7 shrink-0 ${className}`.trimEnd()}
      disabled={busy}
      onClick={() => {
        query.refetch();
      }}
    >
      {busy ? "Retrying..." : "Retry"}
    </Button>
  );
}

interface QueryStateNoticeProps {
  query: QueryStateLike;
  /**
   * Names the read in the failure copy, lower case and without an article:
   * "ZFS pools", "this node's firewall rules".
   */
  subject: string;
  /** Shown when the read succeeded and came back with nothing. */
  empty: ReactNode;
  /**
   * Whatever names the thing being read, for a notice that outlives it — a
   * panel re-rendered with a new disk, a dialog reopened on another
   * interface. See useSettledQueryError. Omit it when the notice is remounted
   * along with its subject, which is the common case.
   */
  identity?: string;
  /** First-load skeleton size. The default suits a short table. */
  skeletonClassName?: string;
}

/**
 * Everything a list can be other than "here are the rows": still loading,
 * failed, genuinely empty, or never read. Render it in place of the table so
 * those stay mutually exclusive and, above all, so a read that failed can
 * never be mistaken for one that returned nothing:
 *
 *     {rows.length > 0 ? <table>…</table> : (
 *       <QueryStateNotice query={q} subject="ZFS pools" empty="No ZFS pools found." />
 *     )}
 *
 * Every branch renders something. That is the point of the component as much
 * as the failure copy is: a query result has more states than the three
 * above — a paused retry, and a disabled query, both report isLoading false,
 * isError false and data undefined all at once — and each call site that
 * hand-rolled this chain fell through such a state into the empty message, or
 * into nothing at all.
 *
 * Deliberately scoped to the no-rows case. When a refetch fails but TanStack
 * still holds the previous rows, the caller shows those rows; taking them off
 * the screen to report a failed refresh would be the worse trade.
 */
export function QueryStateNotice({
  query,
  subject,
  empty,
  identity,
  skeletonClassName = "h-24 w-full",
}: QueryStateNoticeProps) {
  const settledError = useSettledQueryError(query, identity);

  // Ahead of the loading branch: a refetch after a failure re-enters isLoading
  // (TanStack resets a query with no data to "pending"), and the failure is
  // the more useful of the two things to show.
  if (settledError !== null) {
    const serverMessage = describeError(settledError);
    return (
      // role="status" rather than "alert": a tab can hold several of these at
      // once — the node Disks tab draws five — and five assertive
      // interruptions in a row is worse for a screen reader than a polite one.
      <div
        role="status"
        className="flex items-start gap-2 rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive"
      >
        <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" />
        <div className="min-w-0 flex-1">
          <p>{`Could not load ${subject}.`}</p>
          {serverMessage !== "" && (
            <p className="mt-1 font-mono text-xs break-words opacity-90">
              {serverMessage}
            </p>
          )}
          {query.isPaused && <p className="mt-1 text-xs">{PAUSED_RETRY}</p>}
        </div>
        <RetryButton query={query} />
      </div>
    );
  }

  if (query.isLoading) {
    return <Skeleton className={skeletonClassName} />;
  }

  if (query.isSuccess) {
    return <p className="text-sm text-muted-foreground">{empty}</p>;
  }

  // Terminal fallback. Not loading, not failed, not successful — a retry
  // TanStack has paused, a query its `enabled` gate has switched off, or a
  // fetch cancelled on unmount. Returning null here left whole panels blank
  // with nothing on screen to explain why, so this branch has to say
  // something for any state at all.
  return (
    <div className="flex items-start justify-between gap-2">
      <p className="text-sm text-muted-foreground">
        {query.isPaused
          ? // Both gates in the retryer's canContinue() have to reopen —
            // onlineManager.isOnline() and focusManager.isFocused(), the
            // latter being visibilityState !== "hidden" — and neither one
            // re-renders this tree when it shuts, so name both rather than
            // deducing a single cause that may already be out of date.
            `Loading ${subject} is paused. ${PAUSED_RETRY}`
          : `Nexara has not read ${subject} yet.`}
      </p>
      <RetryButton query={query} />
    </div>
  );
}

interface QueryFailureNoteProps {
  query: QueryStateLike;
  /** Completes "Could not load ...", lower case and without an article. */
  subject: string;
  /** See QueryStateNotice. */
  identity?: string;
  className?: string;
}

/**
 * The one-line sibling of QueryStateNotice, for a read whose failure the
 * surrounding UI already has a sane answer to: a picker that falls back to a
 * free-text field, a merged list one of whose sources came back. Replacing
 * that with a block notice would throw away what did load, but leaving it
 * silent makes "there are none" and "we could not ask" the same picture.
 *
 * Renders nothing at all unless the query has settled in failure — loading
 * and empty are the caller's to draw, because only the caller knows what it
 * is drawing them next to.
 */
export function QueryFailureNote({
  query,
  subject,
  identity,
  className = "",
}: QueryFailureNoteProps) {
  const settledError = useSettledQueryError(query, identity);
  if (settledError === null) return null;

  const serverMessage = describeError(settledError);
  const { offer, busy } = retryState(query);
  return (
    <p
      role="status"
      className={`text-xs text-destructive ${className}`.trimEnd()}
    >
      {`Could not load ${subject}${serverMessage !== "" ? `: ${serverMessage}` : "."}`}{" "}
      {offer ? (
        <button
          type="button"
          className="underline underline-offset-2 disabled:no-underline disabled:opacity-60"
          disabled={busy}
          onClick={() => {
            query.refetch();
          }}
        >
          {busy ? "Retrying..." : "Retry"}
        </button>
      ) : (
        PAUSED_RETRY
      )}
    </p>
  );
}
