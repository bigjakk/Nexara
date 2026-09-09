import { useState } from "react";
import { Loader2, LogOut, Monitor, Smartphone, Laptop } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { formatRelativeTime } from "@/lib/format";
import { sessionLabel } from "@/lib/session-label";
import type { UserSession } from "@/types/api";
import { useSessions, useRevokeSession } from "../api/session-queries";

function DeviceIcon({ type }: { type: string }) {
  const className = "h-4 w-4 text-muted-foreground";
  if (type === "mobile") return <Smartphone className={className} />;
  if (type === "desktop") return <Laptop className={className} />;
  return <Monitor className={className} />;
}

export function ActiveSessionsCard() {
  const { data: sessions, isLoading } = useSessions();
  const revokeMutation = useRevokeSession();

  const [confirmCurrent, setConfirmCurrent] = useState<UserSession | null>(
    null,
  );

  // Which row is busy comes from the mutation's own in-flight variables rather
  // than a separate piece of state. One useMutation is shared by every row, so
  // a bare isPending would spin all of them; tracking it locally instead would
  // depend on per-call callbacks, which a second mutate() silently drops.
  const revokingId = revokeMutation.isPending
    ? revokeMutation.variables.id
    : null;
  const error = revokeMutation.error;

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <LogOut className="h-5 w-5" />
          Active Sessions
        </CardTitle>
        <CardDescription>
          Devices currently signed in to your account. Revoke any you do not
          recognise.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        {error && (
          <div className="rounded-md bg-destructive/10 p-3 text-sm text-destructive">
            {error instanceof Error
              ? error.message
              : "Failed to revoke session."}
          </div>
        )}

        {isLoading ? (
          <div className="flex justify-center py-6">
            <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
          </div>
        ) : !sessions || sessions.length === 0 ? (
          // Reachable in practice: an API-key caller has no session row, and a
          // browser whose cookie expired mid-visit sees the same.
          <p className="py-4 text-center text-sm text-muted-foreground">
            No active sessions.
          </p>
        ) : (
          sessions.map((session) => (
            <div
              key={session.id}
              className="flex items-center justify-between gap-4 rounded-lg border p-3"
            >
              <div className="min-w-0 space-y-1">
                <div className="flex items-center gap-2">
                  <DeviceIcon type={session.device_type} />
                  <span className="truncate font-medium">
                    {sessionLabel(session)}
                  </span>
                  {session.is_current && (
                    <Badge variant="secondary" className="text-[10px]">
                      This device
                    </Badge>
                  )}
                </div>
                <p className="text-xs text-muted-foreground">
                  {session.ip_address || "Unknown address"} · last active{" "}
                  {formatRelativeTime(session.last_used_at)}
                </p>
              </div>
              <Button
                variant="ghost"
                size="sm"
                disabled={revokingId === session.id}
                onClick={() => {
                  if (session.is_current) {
                    setConfirmCurrent(session);
                    return;
                  }
                  revokeMutation.mutate(session);
                }}
              >
                {revokingId === session.id ? (
                  <Loader2 className="h-4 w-4 animate-spin" />
                ) : (
                  "Revoke"
                )}
              </Button>
            </div>
          ))
        )}
      </CardContent>

      {/* Revoking the session you are holding signs you out immediately, so it
          confirms first — the same bar every other disruptive action clears. */}
      <AlertDialog
        open={confirmCurrent !== null}
        onOpenChange={(open) => {
          if (!open) setConfirmCurrent(null);
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Sign out this device?</AlertDialogTitle>
            <AlertDialogDescription>
              This is the session you are using right now. Revoking it signs you
              out immediately and you will need to log in again.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              onClick={() => {
                const target = confirmCurrent;
                setConfirmCurrent(null);
                if (target) revokeMutation.mutate(target);
              }}
            >
              Sign out
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </Card>
  );
}
