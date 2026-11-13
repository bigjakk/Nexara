import { Panel } from "@/components/page/Panel";
import { Stat, StatStrip } from "@/components/page/StatStrip";
import {
  useClusterFirewallRules,
  useFirewallOptions,
} from "../api/network-queries";

/**
 * The state of the cluster firewall, above every view of the tab: whether it
 * is on, what it does with traffic no rule matches, and how many rules it
 * holds. Read-only: the Options view changes them.
 *
 * A read that failed says nothing. "Disabled" about a setting nobody managed
 * to read would be a false statement about the cluster's security posture, so
 * an unread value is a dash, and the Options view reports the failure.
 */
export function FirewallStatusStrip({ clusterId }: { clusterId: string }) {
  const opts = useFirewallOptions(clusterId).data;
  const rules = useClusterFirewallRules(clusterId).data;
  const enabled = opts === undefined ? null : opts.enable === 1;
  const disabledRules = (rules ?? []).filter((r) => !r.enable).length;

  return (
    <Panel flush bodyClassName="py-4">
      <StatStrip>
        <Stat
          label="Cluster firewall"
          tone={enabled === true ? "default" : "quiet"}
          value={
            enabled === null ? (
              "—"
            ) : (
              <span className="flex items-center gap-2">
                <span
                  aria-hidden="true"
                  className={
                    enabled
                      ? "h-2.5 w-2.5 shrink-0 rounded-full bg-emerald-500"
                      : "h-2.5 w-2.5 shrink-0 rounded-full ring-[1.5px] ring-inset ring-muted-foreground/70"
                  }
                />
                {enabled ? "Enabled" : "Disabled"}
              </span>
            )
          }
          sub={
            enabled === false
              ? "No rule is enforced while it is off"
              : undefined
          }
        />
        <Stat
          label="Inbound policy"
          tone={opts === undefined ? "quiet" : "default"}
          value={opts === undefined ? "—" : opts.policy_in || "DROP"}
          sub="Traffic no rule matches"
        />
        <Stat
          label="Outbound policy"
          tone={opts === undefined ? "quiet" : "default"}
          value={opts === undefined ? "—" : opts.policy_out || "ACCEPT"}
          sub="Traffic no rule matches"
        />
        <Stat
          label="Cluster rules"
          tone={rules === undefined || rules.length === 0 ? "quiet" : "default"}
          value={rules === undefined ? "—" : String(rules.length)}
          sub={
            disabledRules > 0 ? `${String(disabledRules)} disabled` : undefined
          }
        />
      </StatStrip>
    </Panel>
  );
}
