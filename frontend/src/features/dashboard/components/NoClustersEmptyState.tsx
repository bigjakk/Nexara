import type { LucideIcon } from "lucide-react";
import { useTranslation } from "react-i18next";
import { EmptyState } from "@/components/EmptyState";
import { useCanAddCluster } from "@/hooks/useCanAddCluster";
import { AddClusterDialog } from "./AddClusterDialog";

/**
 * A page's "no cluster yet" state. Only a role that may add a cluster is asked
 * to, in the page's own words and with Add cluster; anyone else is told only
 * that none has been added.
 */
export function NoClustersEmptyState({
  icon,
  description,
}: {
  icon: LucideIcon;
  /** What the page says to a role that may add one. */
  description: string;
}) {
  const { t } = useTranslation("dashboard");
  const canAdd = useCanAddCluster();
  return (
    <EmptyState
      icon={icon}
      title={t("noClustersRegistered")}
      description={canAdd ? description : t("noClustersAddedYet")}
      action={canAdd ? <AddClusterDialog /> : undefined}
    />
  );
}
