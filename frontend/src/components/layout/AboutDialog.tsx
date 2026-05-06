import { Code2, ExternalLink, Heart, Bug, Tag } from "lucide-react";
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

interface AboutDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  version: string | undefined;
}

const REPO_URL = "https://github.com/bigjakk/Nexara";

const STACK = [
  { label: "Go", note: "API + collector + scheduler" },
  { label: "React 19 + TypeScript 5", note: "Frontend SPA" },
  { label: "PostgreSQL + TimescaleDB", note: "Persistence + time-series metrics" },
  { label: "Redis", note: "Cache + pub/sub" },
  { label: "Fiber, sqlc, gorilla/websocket", note: "Go libraries" },
  { label: "Vite, TanStack Query, Shadcn/ui, Zustand", note: "Frontend libraries" },
  { label: "React Flow, Recharts, xterm.js, noVNC", note: "Visualisation & consoles" },
];

export function AboutDialog({ open, onOpenChange, version }: AboutDialogProps) {
  const appTitle = useBrandingStore((s) => s.appTitle);
  const logoUrl = useBrandingStore((s) => s.logoUrl);

  const displayVersion = version ?? "unknown";

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <div className="flex items-center gap-3">
            {logoUrl ? (
              <img
                src={logoUrl}
                alt=""
                className="h-10 w-10 rounded-md object-contain"
              />
            ) : null}
            <div>
              <DialogTitle>About {appTitle}</DialogTitle>
              <DialogDescription>
                Centralized management for Proxmox VE &amp; PBS — like vCenter,
                but open-source.
              </DialogDescription>
            </div>
          </div>
        </DialogHeader>

        <div className="space-y-4">
          <div className="flex items-center gap-2">
            <Tag className="h-4 w-4 text-muted-foreground" />
            <span className="text-sm">Version</span>
            <Badge variant="secondary" className="font-mono">
              {displayVersion}
            </Badge>
          </div>

          <div>
            <p className="mb-2 text-sm font-medium">Built with</p>
            <ul className="space-y-1.5">
              {STACK.map((s) => (
                <li key={s.label} className="flex items-baseline gap-2 text-xs">
                  <span className="font-medium">{s.label}</span>
                  <span className="text-muted-foreground">— {s.note}</span>
                </li>
              ))}
            </ul>
          </div>

          <div className="space-y-2 rounded-md border border-border/60 bg-muted/30 p-3 text-xs">
            <p className="flex items-center gap-1.5">
              <Heart className="h-3.5 w-3.5 text-red-500" />
              <span>
                Free &amp; open-source under{" "}
                <a
                  href={`${REPO_URL}/blob/master/LICENSE`}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="font-medium underline-offset-2 hover:underline"
                >
                  AGPL v3
                </a>
                . Contributions welcome.
              </span>
            </p>
            <p className="text-muted-foreground">
              Thanks to the Proxmox VE / PBS teams and the broader open-source
              community whose work makes this project possible.
            </p>
          </div>

          <div className="flex flex-wrap gap-2">
            <Button asChild variant="outline" size="sm">
              <a href={REPO_URL} target="_blank" rel="noopener noreferrer">
                <Code2 className="mr-1.5 h-3.5 w-3.5" />
                Source on GitHub
                <ExternalLink className="ml-1 h-3 w-3" />
              </a>
            </Button>
            <Button asChild variant="outline" size="sm">
              <a
                href={`${REPO_URL}/releases`}
                target="_blank"
                rel="noopener noreferrer"
              >
                <Tag className="mr-1.5 h-3.5 w-3.5" />
                Releases
                <ExternalLink className="ml-1 h-3 w-3" />
              </a>
            </Button>
            <Button asChild variant="outline" size="sm">
              <a
                href={`${REPO_URL}/issues`}
                target="_blank"
                rel="noopener noreferrer"
              >
                <Bug className="mr-1.5 h-3.5 w-3.5" />
                Report an issue
                <ExternalLink className="ml-1 h-3 w-3" />
              </a>
            </Button>
          </div>
        </div>

        <DialogFooter>
          <Button variant="secondary" onClick={() => { onOpenChange(false); }}>
            Close
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
