import type { ReactNode } from "react";
import { Link } from "react-router-dom";
import { ShieldOff } from "lucide-react";
import { Button } from "@/components/ui/button";
import { usePermissions } from "@/hooks/usePermissions";

interface RequirePermissionProps {
  action: string;
  resource: string;
  children: ReactNode;
}

export function RequirePermission({
  action,
  resource,
  children,
}: RequirePermissionProps) {
  const { hasPermission } = usePermissions();
  if (!hasPermission(action, resource)) {
    return <ForbiddenPage action={action} resource={resource} />;
  }
  return <>{children}</>;
}

function ForbiddenPage({
  action,
  resource,
}: {
  action: string;
  resource: string;
}) {
  return (
    <div className="flex h-full flex-1 flex-col items-center justify-center gap-4 p-8 text-center">
      <ShieldOff className="h-12 w-12 text-muted-foreground" />
      <div className="space-y-2">
        <h1 className="text-2xl font-semibold">Access denied</h1>
        <p className="max-w-md text-sm text-muted-foreground">
          You don&apos;t have permission to view this page. Required permission:{" "}
          <code className="rounded bg-muted px-1.5 py-0.5 font-mono text-xs">
            {action}:{resource}
          </code>
        </p>
        <p className="max-w-md text-sm text-muted-foreground">
          Ask an administrator to grant this permission to your role.
        </p>
      </div>
      <Button asChild>
        <Link to="/">Back to dashboard</Link>
      </Button>
    </div>
  );
}
