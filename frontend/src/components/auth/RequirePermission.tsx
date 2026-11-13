import type { ReactNode } from "react";
import { Link } from "react-router-dom";
import { Button } from "@/components/ui/button";
import { Panel } from "@/components/page/Panel";
import { usePermissions } from "@/hooks/usePermissions";

interface RequirePermissionProps {
  action: string;
  resource: string;
  /**
   * The grant must be install-wide, for a page whose API refuses a
   * cluster-scoped one: otherwise such a holder would reach a page that
   * cannot read anything.
   */
  global?: boolean;
  children: ReactNode;
}

export function RequirePermission({
  action,
  resource,
  global = false,
  children,
}: RequirePermissionProps) {
  const { hasPermission, hasGlobalPermission } = usePermissions();
  if (!(global ? hasGlobalPermission : hasPermission)(action, resource)) {
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
    <div className="flex h-full flex-1 items-center justify-center py-10">
      <Panel className="w-full max-w-md" bodyClassName="px-6 pb-6 pt-6">
        <h1 className="text-xl font-semibold leading-snug">Access denied</h1>
        <p className="mt-1 text-sm text-muted-foreground">
          You don&apos;t have permission to view this page. Ask an administrator
          to grant this permission to your role.
        </p>
        <div className="mt-4 flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1 border-t pt-3 text-sm">
          <span className="text-muted-foreground">Required permission</span>
          <code className="font-mono text-[13px]">
            {action}:{resource}
          </code>
        </div>
        <Button asChild className="mt-5">
          <Link to="/">Back to dashboard</Link>
        </Button>
      </Panel>
    </div>
  );
}
