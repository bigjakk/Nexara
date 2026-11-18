import { useEffect, useState } from "react";
import { Disc, Save } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { EmptyState } from "@/components/EmptyState";
import {
  SettingRow,
  SettingsFooter,
  SettingsSection,
} from "@/components/page/Settings";
import { useVMConfig, useSetVMConfig } from "../api/vm-queries";
import { useSaveOutcome } from "@/hooks/useSaveOutcome";
import { usePermissions } from "@/hooks/usePermissions";
import { useTranslation } from "react-i18next";

interface CloudInitPanelProps {
  clusterId: string;
  vmId: string;
}

export function CloudInitPanel({ clusterId, vmId }: CloudInitPanelProps) {
  const { t } = useTranslation("vms");
  const { data: config, isLoading } = useVMConfig(clusterId, vmId);
  const setConfigMutation = useSetVMConfig();
  const settle = useSaveOutcome();
  // The config route takes manage:vm; without it the fields only show.
  const readOnly = !usePermissions().hasPermission("manage", "vm");

  const [ciuser, setCiuser] = useState("");
  const [cipassword, setCipassword] = useState("");
  const [ipconfig0, setIpconfig0] = useState("");
  const [nameserver, setNameserver] = useState("");
  const [searchdomain, setSearchdomain] = useState("");
  const [sshkeys, setSshkeys] = useState("");

  // Detect cloud-init drive
  const hasCloudInit = config
    ? Object.entries(config).some(
        ([key, val]) =>
          typeof val === "string" &&
          val.includes("cloudinit") &&
          /^(ide|scsi|sata|virtio)\d+$/.test(key),
      )
    : false;

  // Populate fields from config
  useEffect(() => {
    if (!config) return;
    setCiuser(typeof config["ciuser"] === "string" ? config["ciuser"] : "");
    setIpconfig0(
      typeof config["ipconfig0"] === "string" ? config["ipconfig0"] : "",
    );
    setNameserver(
      typeof config["nameserver"] === "string" ? config["nameserver"] : "",
    );
    setSearchdomain(
      typeof config["searchdomain"] === "string" ? config["searchdomain"] : "",
    );
    if (typeof config["sshkeys"] === "string") {
      try {
        setSshkeys(decodeURIComponent(config["sshkeys"]));
      } catch {
        setSshkeys(config["sshkeys"]);
      }
    } else {
      setSshkeys("");
    }
    // Don't set password from config — Proxmox returns a hash, not the actual password.
    setCipassword("");
  }, [config]);

  function handleSave() {
    const fields: Record<string, string> = {};
    if (ciuser) fields["ciuser"] = ciuser;
    if (cipassword) fields["cipassword"] = cipassword;
    if (ipconfig0) fields["ipconfig0"] = ipconfig0;
    if (nameserver) fields["nameserver"] = nameserver;
    if (searchdomain) fields["searchdomain"] = searchdomain;
    if (sshkeys) fields["sshkeys"] = sshkeys;

    if (Object.keys(fields).length === 0) return;

    settle(setConfigMutation.mutateAsync({ clusterId, vmId, fields }), {
      action: t("cloudInitPanel.savingTheCloudInitSettings"),
      onError: () => undefined, // shown below from the mutation's state
    });
  }

  if (isLoading) {
    return <Skeleton className="h-72 w-full max-w-3xl" />;
  }

  if (!hasCloudInit) {
    return (
      <EmptyState
        icon={Disc}
        title={t("cloudInitPanel.noCloudInitDrive")}
        description={t("cloudInitPanel.noCloudInitDriveDetected")}
      />
    );
  }

  return (
    <SettingsSection
      title={t("cloudInitPanel.cloudInit")}
      description={t("cloudInitPanel.whatTheGuestIsGiven")}
      className="max-w-3xl"
    >
      <SettingRow
        label={t("cloudInitPanel.user")}
        description={t("cloudInitPanel.theAccountCloudInitSets")}
        htmlFor="ci-user"
      >
        <Input
          id="ci-user"
          disabled={readOnly}
          value={ciuser}
          onChange={(e) => {
            setCiuser(e.target.value);
          }}
          placeholder="root"
        />
      </SettingRow>
      <SettingRow
        label={t("cloudInitPanel.password")}
        description={t("cloudInitPanel.proxmoxStoresOnlyAHash")}
        htmlFor="ci-password"
      >
        <Input
          id="ci-password"
          disabled={readOnly}
          type="password"
          value={cipassword}
          onChange={(e) => {
            setCipassword(e.target.value);
          }}
          placeholder={t("cloudInitPanel.leaveBlankToKeepCurrent")}
        />
      </SettingRow>
      <SettingRow
        label={t("cloudInitPanel.ipConfigIpconfig0")}
        description={t("cloudInitPanel.theFirstInterfaceSAddress")}
        htmlFor="ci-ipconfig"
      >
        <Input
          id="ci-ipconfig"
          disabled={readOnly}
          value={ipconfig0}
          onChange={(e) => {
            setIpconfig0(e.target.value);
          }}
          placeholder={t("cloudInitPanel.ipDhcpOrIp192")}
          className="font-mono"
        />
      </SettingRow>
      <SettingRow
        label={t("cloudInitPanel.dnsServer")}
        description={t("cloudInitPanel.emptyUsesTheHostS")}
        htmlFor="ci-nameserver"
      >
        <Input
          id="ci-nameserver"
          disabled={readOnly}
          value={nameserver}
          onChange={(e) => {
            setNameserver(e.target.value);
          }}
          placeholder="8.8.8.8"
        />
      </SettingRow>
      <SettingRow
        label={t("cloudInitPanel.searchDomain")}
        description={t("cloudInitPanel.emptyUsesTheHostS")}
        htmlFor="ci-searchdomain"
      >
        <Input
          id="ci-searchdomain"
          disabled={readOnly}
          value={searchdomain}
          onChange={(e) => {
            setSearchdomain(e.target.value);
          }}
          placeholder="example.com"
        />
      </SettingRow>
      <SettingRow
        label={t("cloudInitPanel.sshPublicKeys")}
        description={t("cloudInitPanel.oneKeyPerLineThey")}
        htmlFor="ci-sshkeys"
        className="sm:grid-cols-1"
      >
        <textarea
          id="ci-sshkeys"
          disabled={readOnly}
          value={sshkeys}
          onChange={(e) => {
            setSshkeys(e.target.value);
          }}
          placeholder="ssh-rsa AAAA... user@host"
          rows={4}
          className="flex w-full rounded-md border border-input bg-transparent px-3 py-2 font-mono text-[12.5px] placeholder:text-muted-foreground focus-visible:outline-hidden focus-visible:ring-1 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50"
        />
      </SettingRow>

      {!readOnly && (
        <SettingsFooter>
          {setConfigMutation.isError && (
            <p className="mr-auto text-sm text-destructive">
              {setConfigMutation.error.message}
            </p>
          )}
          {setConfigMutation.isSuccess && (
            <p className="mr-auto text-sm text-muted-foreground">
              {t("cloudInitPanel.cloudInitConfigurationSaved")}
            </p>
          )}
          <Button
            className="gap-2"
            onClick={handleSave}
            disabled={setConfigMutation.isPending}
          >
            <Save className="h-4 w-4" />
            {setConfigMutation.isPending
              ? t("cloudInitPanel.saving")
              : t("cloudInitPanel.save")}
          </Button>
        </SettingsFooter>
      )}
    </SettingsSection>
  );
}
