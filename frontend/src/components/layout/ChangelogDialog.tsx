import { ExternalLink, Loader2, Sparkles } from "lucide-react";
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
                  : `Highlights from the last ${String(entries.length)} releases.`}
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
          <div className="max-h-[60vh] space-y-6 overflow-y-auto pr-1">
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
              </section>
            ))}
          </div>
        )}

        <DialogFooter>
          <Button onClick={() => { onOpenChange(false); }}>Got it</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
