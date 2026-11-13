import { formatBytes } from "@/lib/format";
import { useVeeamSessionTasks } from "../api/backup-queries";
import { StateText } from "../components/StateText";
import { resultTone } from "./veeam-format";

interface VeeamTaskTableProps {
  serverId: string;
  /** The run to break down. Empty means there is nothing to open. */
  sessionVeeamId: string;
  /** True while the row is expanded. Nothing is fetched until it is. */
  enabled: boolean;
  /**
   * Whether the run is still in flight.
   *
   * Load-bearing for the empty state, not decoration: Veeam reports no task
   * rows for a run that has not finished, so "nothing yet" and "no detail at
   * all" are different facts that render identically without this.
   */
  running: boolean;
}

function formatTime(value: string | null): string {
  if (value == null || value === "") return "—";
  const parsed = new Date(value);
  if (Number.isNaN(parsed.getTime())) return "—";
  return parsed.toLocaleTimeString();
}

/**
 * Which guests a run processed, and which of them failed.
 *
 * This is the question the Veeam console answers and a job-level result does
 * not: "Failed" on a job covering eleven guests says nothing about which one.
 * Nothing else in the API reaches it either — a Proxmox job's object list is
 * unreadable, so a run's task sessions are the only route.
 */
export function VeeamTaskTable({
  serverId,
  sessionVeeamId,
  enabled,
  running,
}: VeeamTaskTableProps) {
  const { data, isLoading, isError, error } = useVeeamSessionTasks(
    serverId,
    sessionVeeamId,
    enabled && sessionVeeamId !== "",
  );

  if (sessionVeeamId === "") {
    return (
      <p className="text-xs text-muted-foreground">
        This job has not run yet, so there is no per-guest detail to show.
      </p>
    );
  }

  if (isLoading) {
    return <p className="text-xs text-muted-foreground">Loading guests…</p>;
  }

  if (isError) {
    return (
      <p className="text-xs text-destructive">
        Could not read the per-guest detail from the Veeam server.{" "}
        {error instanceof Error ? error.message : ""}
      </p>
    );
  }

  const tasks = data ?? [];

  if (tasks.length === 0) {
    return (
      <p className="text-xs text-muted-foreground">
        {running
          ? "Veeam reports a guest only once its task finishes, so this fills in as the run progresses."
          : "Veeam kept no per-guest detail for this run."}
      </p>
    );
  }

  return (
    <div>
      <p className="mb-2 text-[13.5px] font-semibold">
        Guests in this run{" "}
        <span className="font-normal text-muted-foreground">
          ({tasks.length})
        </span>
      </p>
      <div className="overflow-x-auto">
        <table className="w-full text-xs">
          <thead>
            <tr className="text-left text-muted-foreground">
              <th className="pb-1.5 pr-4 font-normal">Guest</th>
              <th className="pb-1.5 pr-4 font-normal">Result</th>
              <th className="pb-1.5 pr-4 font-normal">Started</th>
              <th className="pb-1.5 pr-4 font-normal">Finished</th>
              <th className="pb-1.5 pr-4 font-normal">Duration</th>
              <th className="pb-1.5 text-right font-normal">Transferred</th>
            </tr>
          </thead>
          <tbody>
            {tasks.map((task) => (
              <tr key={task.id} className="border-t">
                <td className="py-1.5 pr-4">
                  <span className="font-medium">{task.name}</span>
                  {/* The VMID, where the name resolved to exactly one guest.
                      Deliberately not a link: the VM route keys on vms.id,
                      which the collector churns on every delete-and-reinsert,
                      and the rest of this feature keys guests on the stable
                      (cluster, vmid) pair for exactly that reason.

                      Its ABSENCE is information too. A task session carries no
                      uuid, so an unresolved or ambiguous name is left bare
                      rather than guessed at — and a guest Nexara cannot place
                      is the most interesting row here, not one to hide. */}
                  {task.vmid != null && (
                    <span className="ml-2 font-mono text-muted-foreground">
                      {task.vmid}
                    </span>
                  )}
                </td>
                <td className="py-1.5 pr-4">
                  {task.result === "" ? (
                    <span className="text-muted-foreground">
                      {task.state || "—"}
                    </span>
                  ) : (
                    <StateText tone={resultTone(task.result)}>
                      {task.result}
                    </StateText>
                  )}
                </td>
                <td className="py-1.5 pr-4 tabular-nums">
                  {formatTime(task.creation_time)}
                </td>
                <td className="py-1.5 pr-4 tabular-nums">
                  {formatTime(task.end_time)}
                </td>
                <td className="py-1.5 pr-4 tabular-nums">
                  {task.duration || "—"}
                </td>
                <td className="py-1.5 text-right tabular-nums">
                  {formatBytes(task.transferred_size)}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      {/* The per-guest message, where there is one. This is the payload: the
          reason a specific guest failed, which the run's own result never
          carries. */}
      {tasks.some((t) => t.result === "Failed" && t.result_message !== "") && (
        <ul className="mt-2 space-y-1">
          {tasks
            .filter((t) => t.result === "Failed" && t.result_message !== "")
            .map((t) => (
              <li key={t.id} className="text-xs text-muted-foreground">
                <span className="font-medium">{t.name}</span>
                {": "}
                {t.result_message}
              </li>
            ))}
        </ul>
      )}
    </div>
  );
}
