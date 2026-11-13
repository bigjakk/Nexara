import { Component } from "react";
import type { ErrorInfo, ReactNode } from "react";
import { Button } from "@/components/ui/button";

interface Props {
  children: ReactNode;
}

interface State {
  hasError: boolean;
  error: Error | null;
}

const CHUNK_RELOAD_KEY = "nexara-chunk-reload-at";

// A deploy replaces the content-hashed chunk files, so a still-open tab's
// lazy route imports start rejecting — and React.lazy caches the rejection,
// making any in-place retry useless. Only a full reload (fresh index.html →
// fresh chunk names) recovers.
function isStaleChunkError(error: Error): boolean {
  return /failed to fetch dynamically imported module|error loading dynamically imported module|loading chunk \S+ failed/i.test(
    error.message,
  );
}

export class ErrorBoundary extends Component<Props, State> {
  constructor(props: Props) {
    super(props);
    this.state = { hasError: false, error: null };
  }

  static getDerivedStateFromError(error: Error): State {
    return { hasError: true, error };
  }

  componentDidCatch(error: Error, errorInfo: ErrorInfo): void {
    console.error("ErrorBoundary caught:", error, errorInfo);
    if (isStaleChunkError(error)) {
      this.tryAutoReload();
    }
  }

  // One transparent reload per 30s window: enough to heal the common
  // post-deploy case invisibly, while a genuinely broken asset falls
  // through to the manual UI instead of reload-looping.
  tryAutoReload(): void {
    try {
      const last = Number(sessionStorage.getItem(CHUNK_RELOAD_KEY) ?? "0");
      if (Date.now() - last < 30_000) return;
      sessionStorage.setItem(CHUNK_RELOAD_KEY, String(Date.now()));
    } catch {
      return;
    }
    window.location.reload();
  }

  handleRetry = (): void => {
    if (this.state.error && isStaleChunkError(this.state.error)) {
      window.location.reload();
      return;
    }
    this.setState({ hasError: false, error: null });
  };

  render(): ReactNode {
    if (this.state.hasError) {
      const staleChunk = this.state.error
        ? isStaleChunkError(this.state.error)
        : false;
      const message = this.state.error?.message ?? "";
      return (
        <div className="flex h-full min-h-60 items-center justify-center p-4">
          <div
            role="alert"
            className="panel w-full max-w-lg rounded-[10px] px-6 py-6"
          >
            <h2 className="text-base font-semibold [font-stretch:108%]">
              {staleChunk
                ? "A new version of Nexara is ready"
                : "This page could not be shown"}
            </h2>
            <p className="mt-1.5 text-sm text-muted-foreground">
              {staleChunk
                ? "It was deployed while this tab was open. Reload to pick it up."
                : "It failed while it was being drawn. Retry, or open another page."}
            </p>
            {!staleChunk && message !== "" && (
              <pre className="mt-3 max-h-40 overflow-auto rounded-[7px] bg-foreground/[0.06] px-3 py-2 font-mono text-[12.5px] leading-[1.55] break-words whitespace-pre-wrap dark:bg-black/30">
                {message}
              </pre>
            )}
            <Button
              onClick={this.handleRetry}
              variant="outline"
              size="sm"
              className="mt-5"
            >
              {staleChunk ? "Reload" : "Retry"}
            </Button>
          </div>
        </div>
      );
    }

    return this.props.children;
  }
}
