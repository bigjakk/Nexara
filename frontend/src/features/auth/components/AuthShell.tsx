import type { ReactNode } from "react";
import { Server } from "lucide-react";
import { Panel } from "@/components/page/Panel";

/**
 * The frame of a page outside the app shell (sign-in, first-run setup, the
 * single sign-on return): a centred column on the canvas with the wordmark,
 * then one flat panel that holds the page's heading and its form.
 */
export function AuthShell({
  banner,
  title,
  description,
  children,
}: {
  /** Above the panel at the column's width: the insecure-connection warning. */
  banner?: ReactNode;
  title: ReactNode;
  /** One sentence under the heading: what this step asks for. */
  description?: ReactNode;
  children?: ReactNode;
}) {
  return (
    <div className="flex min-h-screen flex-col items-center justify-center px-4 py-10">
      <div className="w-full max-w-md">
        <div className="mb-5 flex items-center gap-2.5 px-1">
          <span
            aria-hidden="true"
            className="grid h-9 w-9 shrink-0 place-items-center rounded-lg bg-action text-action-foreground"
          >
            <Server className="h-5 w-5" />
          </span>
          <span className="text-[22px] font-[650] leading-none tracking-[-0.012em] [font-stretch:112%]">
            Nexara
          </span>
        </div>
        {banner}
        <Panel bodyClassName="px-6 pb-6 pt-6">
          <h1 className="text-xl font-semibold leading-snug">{title}</h1>
          {description !== undefined && (
            <p className="mt-1 text-sm text-muted-foreground">{description}</p>
          )}
          {children !== undefined && <div className="mt-5">{children}</div>}
        </Panel>
      </div>
    </div>
  );
}
