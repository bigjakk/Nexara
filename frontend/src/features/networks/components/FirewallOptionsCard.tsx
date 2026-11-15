import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Badge } from "@/components/ui/badge";
import { Panel } from "@/components/page/Panel";
import { SettingRow } from "@/components/page/Settings";
import { QueryStateNotice } from "@/components/QueryStateNotice";
import { ConfirmDeleteDialog } from "@/components/ConfirmDeleteDialog";
import {
  useFirewallOptions,
  useSetFirewallOptions,
} from "../api/network-queries";

interface FirewallOptionsCardProps {
  clusterId: string;
}

const POLICIES = ["ACCEPT", "DROP", "REJECT"] as const;

export function FirewallOptionsCard({ clusterId }: FirewallOptionsCardProps) {
  const optionsQuery = useFirewallOptions(clusterId);
  const opts = optionsQuery.data;
  const setOpts = useSetFirewallOptions(clusterId);
  const { t } = useTranslation("networks");
  // Open while turning the cluster firewall off waits on its confirmation.
  const [confirmDisable, setConfirmDisable] = useState<true | null>(null);

  // `opts?.enable === 1` is false for a read that failed as readily as for a
  // firewall that is off, and this badge is a statement about the cluster's
  // security posture. Say nothing rather than say "Disabled" about a setting
  // nobody managed to read — and withhold the controls, which would otherwise
  // offer to toggle from a state that was guessed.
  if (!opts) {
    return (
      <Panel title="Options">
        <QueryStateNotice
          query={optionsQuery}
          subject="the cluster firewall options"
          empty="Proxmox returned no firewall options for this cluster."
          skeletonClassName="h-20 w-full"
        />
      </Panel>
    );
  }

  const isEnabled = opts.enable === 1;

  return (
    <Panel
      title="Options"
      aside="Each change is saved as soon as you make it"
      className="max-w-4xl"
    >
      <div className="divide-y">
        <SettingRow
          label="Cluster firewall"
          description="While it is off, no cluster, node or guest rule is enforced."
        >
          <Badge variant={isEnabled ? "healthy" : "secondary"}>
            {isEnabled ? "Enabled" : "Disabled"}
          </Badge>
          {/* Turning it off stops every rule being enforced, at once: it asks
              first. Turning it on does not. */}
          <Button
            variant="outline"
            size="sm"
            className={
              isEnabled
                ? "border-destructive/50 text-destructive hover:bg-destructive/10 hover:text-destructive"
                : undefined
            }
            onClick={() => {
              if (isEnabled) setConfirmDisable(true);
              else setOpts.mutate({ enable: 1 });
            }}
            disabled={setOpts.isPending}
          >
            {isEnabled ? t("disableFirewall") : "Enable Firewall"}
          </Button>
          <ConfirmDeleteDialog
            target={confirmDisable}
            onClose={() => {
              setConfirmDisable(null);
            }}
            onConfirm={() => {
              setOpts.mutate({ enable: 0 });
            }}
            title={() => t("disableClusterFirewallTitle")}
            description={() => t("disableClusterFirewallDesc")}
            confirmLabel={t("disableFirewall")}
          />
        </SettingRow>
        <SettingRow
          label="Inbound policy"
          description="What happens to incoming traffic no rule matches."
          htmlFor="firewall-policy-in"
        >
          <Select
            value={opts.policy_in || "DROP"}
            onValueChange={(val) => {
              setOpts.mutate({ policy_in: val });
            }}
          >
            <SelectTrigger id="firewall-policy-in" className="sm:w-44">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {POLICIES.map((policy) => (
                <SelectItem key={policy} value={policy}>
                  {policy}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </SettingRow>
        <SettingRow
          label="Outbound policy"
          description="What happens to outgoing traffic no rule matches."
          htmlFor="firewall-policy-out"
        >
          <Select
            value={opts.policy_out || "ACCEPT"}
            onValueChange={(val) => {
              setOpts.mutate({ policy_out: val });
            }}
          >
            <SelectTrigger id="firewall-policy-out" className="sm:w-44">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {POLICIES.map((policy) => (
                <SelectItem key={policy} value={policy}>
                  {policy}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </SettingRow>
      </div>
    </Panel>
  );
}
