import { AlertTriangle, Bell, CheckCircle, Clock } from "lucide-react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { cn } from "@/lib/utils";
import { useAlertSummary } from "../api/alert-queries";

// A count of zero wears no colour: only alerts that exist draw the eye.
const QUIET = "text-muted-foreground";

export function AlertSummaryCard() {
  const { data: summary } = useAlertSummary();

  const firing = summary?.firing_count ?? 0;
  const pending = summary?.pending_count ?? 0;
  const acknowledged = summary?.acknowledged_count ?? 0;

  return (
    <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
      <Card>
        <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
          <CardTitle className="text-sm font-medium">Firing</CardTitle>
          <AlertTriangle
            className={cn("h-4 w-4", firing > 0 ? "text-red-500" : QUIET)}
          />
        </CardHeader>
        <CardContent>
          <div
            className={cn(
              "text-2xl font-bold tracking-tight",
              firing > 0 && "text-red-600 dark:text-red-400",
            )}
          >
            {firing}
          </div>
          {summary && summary.critical_firing > 0 && (
            <p className="text-xs text-muted-foreground">
              {summary.critical_firing} critical
            </p>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
          <CardTitle className="text-sm font-medium">Pending</CardTitle>
          <Clock
            className={cn("h-4 w-4", pending > 0 ? "text-amber-500" : QUIET)}
          />
        </CardHeader>
        <CardContent>
          <div
            className={cn(
              "text-2xl font-bold tracking-tight",
              pending > 0 && "text-amber-600 dark:text-amber-400",
            )}
          >
            {pending}
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
          <CardTitle className="text-sm font-medium">Acknowledged</CardTitle>
          <Bell className={cn("h-4 w-4", QUIET)} />
        </CardHeader>
        <CardContent>
          <div className="text-2xl font-bold tracking-tight">
            {acknowledged}
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
          <CardTitle className="text-sm font-medium">Total Active</CardTitle>
          <CheckCircle className="h-4 w-4 text-muted-foreground" />
        </CardHeader>
        <CardContent>
          <div className="text-2xl font-bold tracking-tight">
            {firing + pending + acknowledged}
          </div>
        </CardContent>
      </Card>
    </div>
  );
}
