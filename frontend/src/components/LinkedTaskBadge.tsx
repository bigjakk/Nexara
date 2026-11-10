import { useTranslation } from "react-i18next";
import { ArrowRightLeft, ShieldCheck } from "lucide-react";

import type { LinkedTaskRole } from "@/components/layout/linked-task";
import { cn } from "@/lib/utils";

/**
 * The pill that says which part of an HA action or a migration a row is (see
 * LinkedTaskRole). Renders nothing for a row that is an action of its own.
 * `requester` is who asked the HA manager for a worker task, when known.
 * `compact` lets a narrow cell cut the pill short rather than the name beside
 * it; the whole text stays in its tooltip.
 */
export function LinkedTaskBadge({
  role,
  requester = "",
  compact = false,
}: {
  role: LinkedTaskRole | null;
  requester?: string;
  compact?: boolean;
}) {
  const { t } = useTranslation("common");
  if (!role) return null;
  const Icon = role === "migration-start" ? ArrowRightLeft : ShieldCheck;
  const label =
    role === "ha-request"
      ? t("linkedTask.haRequest")
      : role === "migration-start"
        ? t("linkedTask.migrationStart")
        : requester
          ? t("linkedTask.haManagerFor", { who: requester })
          : t("linkedTask.haManager");
  return (
    <span
      title={label}
      className={cn(
        "inline-flex items-center gap-0.5 rounded-full bg-violet-500/10 px-1.5 py-0.5 text-[10px] leading-none font-medium text-violet-600 dark:text-violet-400",
        compact ? "min-w-0" : "shrink-0",
      )}
    >
      <Icon className="h-2.5 w-2.5 shrink-0" />
      <span className={compact ? "truncate" : undefined}>{label}</span>
    </span>
  );
}
