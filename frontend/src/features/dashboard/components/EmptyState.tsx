import { useTranslation } from "react-i18next";
import { ServerCrash } from "lucide-react";
import { AddClusterDialog } from "./AddClusterDialog";

export function EmptyState() {
  const { t } = useTranslation("dashboard");
  return (
    <div className="flex flex-col items-center justify-center rounded-lg border border-dashed p-12 text-center">
      <ServerCrash className="h-12 w-12 text-muted-foreground" />
      <h3 className="mt-4 text-lg font-semibold">{t("noClustersRegistered")}</h3>
      <p className="mt-2 text-sm text-muted-foreground">
        {t("addClusterToGetStarted")}
      </p>
      <div className="mt-6">
        <AddClusterDialog />
      </div>
    </div>
  );
}
