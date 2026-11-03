import { toast } from "sonner";
import type { UpsCommandRun } from "../types/ups";

/**
 * A run's first answer, as a toast: upsd's refusal is a run that failed, not
 * an HTTP error. Called from the mutation hooks, which run even once the page
 * that sent the run is gone; the run history shows the driver's later answer.
 */
export function reportUpsRun(run: UpsCommandRun): void {
  const what = run.kind === "set" ? `Setting ${run.name}` : run.name;
  switch (run.status) {
    case "failed":
      toast.error(`${what} failed: ${run.result}`);
      break;
    case "unconfirmed":
      toast.info(`${what}: ${run.result}`);
      break;
    case "succeeded":
      toast.success(`${what}: done`);
      break;
    default:
      toast.success(`${what} sent, waiting for the driver's answer`);
  }
}
