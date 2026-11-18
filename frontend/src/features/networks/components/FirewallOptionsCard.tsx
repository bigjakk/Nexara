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
import { usePermissions } from "@/hooks/usePermissions";
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
  // The options route takes manage:network on the cluster; without it the
  // card shows the settings read-only.
  const canEdit = usePermissions().canManage("network");

  // `opts?.enable === 1` is false for a read that failed as readily as for a
  // firewall that is off, and this badge is a statement about the cluster's
  // security posture. Say nothing rather than say "Disabled" about a setting
  // nobody managed to read — and withhold the controls, which would otherwise
  // offer to toggle from a state that was guessed.
  if (!opts) {
    return (
      <Panel title={t("firewallOptionsCard.options")}>
        <QueryStateNotice
          query={optionsQuery}
          subject={t("firewallOptionsCard.theClusterFirewallOptions")}
          empty={t("firewallOptionsCard.proxmoxReturnedNoFirewallOptions")}
          skeletonClassName="h-20 w-full"
        />
      </Panel>
    );
  }

  const isEnabled = opts.enable === 1;

  return (
    <Panel
      title={t("firewallOptionsCard.options")}
      aside={canEdit ? t("firewallOptionsCard.eachChangeIsSavedAs") : undefined}
      className="max-w-4xl"
    >
      <div className="divide-y">
        <SettingRow
          label={t("firewallOptionsCard.clusterFirewall")}
          description={t("firewallOptionsCard.whileItIsOffNo")}
        >
          <Badge variant={isEnabled ? "healthy" : "secondary"}>
            {isEnabled
              ? t("firewallOptionsCard.enabled")
              : t("firewallOptionsCard.disabled")}
          </Badge>
          {/* Turning it off stops every rule being enforced, at once: it asks
              first. Turning it on does not. */}
          {canEdit && (
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
              {isEnabled
                ? t("disableFirewall")
                : t("firewallOptionsCard.enableFirewall")}
            </Button>
          )}
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
          label={t("firewallOptionsCard.inboundPolicy")}
          description={t("firewallOptionsCard.whatHappensToIncomingTraffic")}
          htmlFor="firewall-policy-in"
        >
          <Select
            value={opts.policy_in || "DROP"}
            onValueChange={(val) => {
              setOpts.mutate({ policy_in: val });
            }}
            disabled={!canEdit}
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
          label={t("firewallOptionsCard.outboundPolicy")}
          description={t("firewallOptionsCard.whatHappensToOutgoingTraffic")}
          htmlFor="firewall-policy-out"
        >
          <Select
            value={opts.policy_out || "ACCEPT"}
            onValueChange={(val) => {
              setOpts.mutate({ policy_out: val });
            }}
            disabled={!canEdit}
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
