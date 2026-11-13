import { Loader2 } from "lucide-react";

/** What a page shows while its code is still arriving. */
export function PageLoader() {
  return (
    <div
      role="status"
      className="flex h-full min-h-40 items-center justify-center gap-2 text-sm text-muted-foreground"
    >
      <Loader2 aria-hidden="true" className="h-4 w-4 animate-spin" />
      Loading
    </div>
  );
}
