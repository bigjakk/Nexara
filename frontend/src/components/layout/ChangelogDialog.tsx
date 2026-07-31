import { useCallback, useEffect, useRef, useState } from "react";
import { ChevronDown, ExternalLink, Loader2, Sparkles } from "lucide-react";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { useBrandingStore } from "@/stores/branding-store";
import type { ChangelogEntry } from "@/lib/changelog";

interface ChangelogDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  entries: ChangelogEntry[];
  loading?: boolean;
  repoReleasesUrl?: string;
}

export function ChangelogDialog({
  open,
  onOpenChange,
  entries,
  loading = false,
  repoReleasesUrl,
}: ChangelogDialogProps) {
  const appTitle = useBrandingStore((s) => s.appTitle);

  const isEmpty = !loading && entries.length === 0;

  // The scroll region carries no visible scrollbar on platforms that use
  // overlay scrollbars, and the fold usually lands cleanly between cards — so
  // a list with far more below it reads as complete. Track whether there is
  // anything further down and show an explicit cue.
  const scrollRef = useRef<HTMLDivElement | null>(null);
  const [hasMoreBelow, setHasMoreBelow] = useState(false);

  const syncScrollCue = useCallback(() => {
    const el = scrollRef.current;
    if (!el) return;
    // 2px tolerance: fractional layout heights otherwise leave the cue stuck on
    // at the very bottom of the list.
    setHasMoreBelow(el.scrollHeight - el.scrollTop - el.clientHeight > 2);
  }, []);

  useEffect(() => {
    if (!open) return;
    // Entries arrive after the dialog opens, so measure once they render.
    const id = requestAnimationFrame(syncScrollCue);
    // The list height is viewport-derived, so resizing changes whether there is
    // anything below the fold. Without this the cue goes stale the moment the
    // window changes size and only corrects itself on the next scroll — which
    // is precisely the interaction it exists to prompt.
    window.addEventListener("resize", syncScrollCue);
    return () => {
      cancelAnimationFrame(id);
      window.removeEventListener("resize", syncScrollCue);
    };
  }, [open, entries, loading, syncScrollCue]);

  const totalHighlights = entries.reduce((n, e) => n + e.highlights.length, 0);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-2xl">
        <DialogHeader>
          <div className="flex items-center gap-2">
            <Sparkles className="h-5 w-5 text-primary" />
            <DialogTitle>What&apos;s new in {appTitle}</DialogTitle>
          </div>
          <DialogDescription>
            {loading
              ? "Loading release notes…"
              : isEmpty
                ? "No release notes available right now."
                : entries.length === 1
                  ? "Highlights from this release."
                  : `${String(totalHighlights)} highlights across the last ${String(entries.length)} releases.`}
          </DialogDescription>
        </DialogHeader>

        {loading ? (
          <div className="flex h-32 items-center justify-center text-muted-foreground">
            <Loader2 className="h-5 w-5 animate-spin" />
          </div>
        ) : isEmpty ? (
          <div className="rounded-md border border-border/60 bg-muted/30 p-4 text-sm text-muted-foreground">
            <p>
              We couldn&apos;t reach GitHub to load release notes. You can view
              them directly on the releases page.
            </p>
            {repoReleasesUrl ? (
              <Button asChild variant="outline" size="sm" className="mt-3">
                <a
                  href={repoReleasesUrl}
                  target="_blank"
                  rel="noopener noreferrer"
                >
                  View releases on GitHub
                  <ExternalLink className="ml-1 h-3 w-3" />
                </a>
              </Button>
            ) : null}
          </div>
        ) : (
          <div className="relative">
            {/* The scroller stays in normal flow and carries its own
                max-height. Sizing it from the dialog instead (flex-1 or a 1fr
                grid row) does not work here: DialogContent is display:grid with
                height:auto, so a 1fr row has no free space to receive and
                collapses to 0.

                The height tracks the viewport rather than being a flat 60vh, so
                a tall window shows most of the list instead of a third of it,
                while the 10rem subtracted for the header, footer, gaps and
                padding keeps the whole dialog on screen on short ones. */}
            <div
              ref={scrollRef}
              onScroll={syncScrollCue}
              className="max-h-[calc(85vh-10rem)] space-y-6 overflow-y-auto pr-1"
            >
              {entries.map((entry) => (
                <section key={entry.version} className="space-y-3">
                  <header className="flex flex-wrap items-center gap-2">
                    <Badge variant="secondary" className="font-mono">
                      v{entry.version}
                    </Badge>
                    {entry.date ? (
                      <span className="text-xs text-muted-foreground">
                        {entry.date}
                      </span>
                    ) : null}
                    {entry.url ? (
                      <a
                        href={entry.url}
                        target="_blank"
                        rel="noopener noreferrer"
                        className="ml-auto inline-flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground hover:underline"
                      >
                        Full release notes
                        <ExternalLink className="h-3 w-3" />
                      </a>
                    ) : null}
                  </header>
                  <ul className="space-y-2">
                    {entry.highlights.map((h, i) => (
                      <li
                        key={`${entry.version}-${String(i)}`}
                        className="rounded-md border border-border/60 bg-muted/30 p-3"
                      >
                        <p className="text-sm font-medium leading-tight">
                          {h.title}
                        </p>
                        {h.description ? (
                          <p className="mt-1 text-xs text-muted-foreground">
                            {h.description}
                          </p>
                        ) : null}
                      </li>
                    ))}
                  </ul>
                  {entry.more_count ? (
                    <p className="text-xs text-muted-foreground">
                      +{entry.more_count} more in the full release notes.
                    </p>
                  ) : null}
                </section>
              ))}
            </div>

            {/* Scroll cue. The list has no visible scrollbar on platforms with
                overlay scrollbars and the fold normally falls between cards, so
                without this a partially-shown list looks like the whole list. */}
            {hasMoreBelow ? (
              <div className="pointer-events-none absolute inset-x-0 bottom-0 flex justify-center bg-gradient-to-t from-background via-background/80 to-transparent pt-6 pb-1">
                <span className="flex items-center gap-1 text-xs text-muted-foreground">
                  <ChevronDown className="h-3 w-3" />
                  Scroll for more
                </span>
              </div>
            ) : null}
          </div>
        )}

        <DialogFooter>
          <Button
            onClick={() => {
              onOpenChange(false);
            }}
          >
            Got it
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
