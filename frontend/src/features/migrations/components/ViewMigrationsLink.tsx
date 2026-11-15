import { Link } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import { usePermissions } from "@/hooks/usePermissions";

/**
 * "View migrations": the Migrations page, for whoever may read it
 * (view:migration, as GET /migrations filters by). `onNavigate` closes the
 * dialog it sits in.
 */
export function ViewMigrationsLink({
  onNavigate,
}: {
  onNavigate?: () => void;
}) {
  const { t } = useTranslation("common");
  const { canView } = usePermissions();
  if (!canView("migration")) return null;
  return (
    <Button variant="outline" size="sm" asChild>
      <Link to="/migrations" onClick={onNavigate}>
        {t("viewMigrations")}
      </Link>
    </Button>
  );
}
