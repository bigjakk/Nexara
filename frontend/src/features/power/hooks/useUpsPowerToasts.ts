import { useEffect, useRef } from "react";
import { useNavigate } from "react-router-dom";
import { toast } from "sonner";
import { usePermissions } from "@/hooks/usePermissions";
import { useUpsDevices } from "../api/ups-queries";
import { formatRuntime, upsStatus } from "../lib/ups-state";
import type { UpsState } from "../types/ups";

/**
 * Toasts a UPS going on battery or low, once per move it sees between two
 * fetches, for view:ups holders. Mounted once, in the app shell.
 *
 * The first fetch is a baseline: a UPS already on battery when the app opened
 * is for the Power page and the health pill to show, not news. A UPS Nexara
 * cannot read ("unknown") is skipped rather than recorded, so an outage seen on
 * both sides of a gap in contact toasts once.
 */
export function useUpsPowerToasts(): void {
  const { hasGlobalPermission } = usePermissions();
  const canView = hasGlobalPermission("view", "ups");
  const { data } = useUpsDevices(canView);
  const navigate = useNavigate();
  const known = useRef(new Map<string, UpsState>());
  const seeded = useRef(false);

  useEffect(() => {
    if (!canView || data === undefined) return;
    const baseline = !seeded.current;
    seeded.current = true;
    for (const d of data) {
      if (d.state === "unknown") continue;
      const was = known.current.get(d.id);
      known.current.set(d.id, d.state);
      if (baseline || was === undefined || was === d.state) continue;
      const left = d.battery_runtime === null ? "" : ` · ${formatRuntime(d.battery_runtime)} left`;
      const view = { label: "View", onClick: () => void navigate(`/power/${d.id}`) };
      if (d.state === "low_battery") {
        toast.error(`${d.name}: ${upsStatus(d).label}${left}`, { action: view });
      } else if (d.state === "on_battery" && was !== "low_battery") {
        toast.warning(`${d.name} is on battery${left}`, { action: view });
      }
    }
  }, [canView, data, navigate]);
}
