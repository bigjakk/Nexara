import { Component } from "react";
import type { ErrorInfo, ReactNode } from "react";
import { AlertTriangle } from "lucide-react";
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
      return (
        <div className="flex h-full flex-col items-center justify-center gap-4 p-8">
          <AlertTriangle className="h-12 w-12 text-destructive" />
          <h2 className="text-lg font-semibold">
            {staleChunk ? "Update available" : "Something went wrong"}
          </h2>
          <p className="text-sm text-muted-foreground max-w-md text-center">
            {staleChunk
              ? "A new version of Nexara was deployed while this tab was open. Reload to pick it up."
              : (this.state.error?.message ?? "An unexpected error occurred.")}
          </p>
          <Button onClick={this.handleRetry} variant="outline">
            {staleChunk ? "Reload" : "Try Again"}
          </Button>
        </div>
      );
    }

    return this.props.children;
  }
}
