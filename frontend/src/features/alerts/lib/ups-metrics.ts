/**
 * The metrics that read the UPS readings (notifications.IsUPSMetric). A rule
 * on one, of any scope, names a UPS in its alerts, so creating or enabling it
 * takes a global view:ups; turning it off does not.
 */
export const UPS_METRICS: ReadonlySet<string> = new Set([
  "ups_on_battery",
  "ups_battery_charge",
  "ups_runtime",
  "ups_load",
  "ups_unreachable",
  "ups_replace_battery",
]);
