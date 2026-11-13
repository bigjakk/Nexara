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

interface CloudInitPanelProps {
  clusterId: string;
  vmId: string;
}

export function CloudInitPanel({ clusterId, vmId }: CloudInitPanelProps) {
  const { data: config, isLoading } = useVMConfig(clusterId, vmId);
  const setConfigMutation = useSetVMConfig();

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

    setConfigMutation.mutate({ clusterId, vmId, fields });
  }

  if (isLoading) {
    return <Skeleton className="h-72 w-full max-w-3xl" />;
  }

  if (!hasCloudInit) {
    return (
      <EmptyState
        icon={Disc}
        title="No cloud-init drive"
        description="No cloud-init drive detected. Add a cloud-init drive (IDE, SCSI, etc.) to this VM on the Hardware tab to configure cloud-init settings."
      />
    );
  }

  return (
    <SettingsSection
      title="Cloud-init"
      description="What the guest is given on its cloud-init drive: an account, its network and the keys that may sign in. Proxmox regenerates the drive when the VM next starts."
      className="max-w-3xl"
    >
      <SettingRow
        label="User"
        description="The account cloud-init sets up. Empty keeps the image's default user."
        htmlFor="ci-user"
      >
        <Input
          id="ci-user"
          value={ciuser}
          onChange={(e) => {
            setCiuser(e.target.value);
          }}
          placeholder="root"
        />
      </SettingRow>
      <SettingRow
        label="Password"
        description="Proxmox stores only a hash, so the current one is never shown."
        htmlFor="ci-password"
      >
        <Input
          id="ci-password"
          type="password"
          value={cipassword}
          onChange={(e) => {
            setCipassword(e.target.value);
          }}
          placeholder="Leave blank to keep current"
        />
      </SettingRow>
      <SettingRow
        label="IP config (ipconfig0)"
        description="The first interface's address: DHCP, or a static address and gateway."
        htmlFor="ci-ipconfig"
      >
        <Input
          id="ci-ipconfig"
          value={ipconfig0}
          onChange={(e) => {
            setIpconfig0(e.target.value);
          }}
          placeholder="ip=dhcp or ip=192.0.2.10/24,gw=192.0.2.1"
          className="font-mono"
        />
      </SettingRow>
      <SettingRow
        label="DNS server"
        description="Empty uses the host's own."
        htmlFor="ci-nameserver"
      >
        <Input
          id="ci-nameserver"
          value={nameserver}
          onChange={(e) => {
            setNameserver(e.target.value);
          }}
          placeholder="8.8.8.8"
        />
      </SettingRow>
      <SettingRow
        label="Search domain"
        description="Empty uses the host's own."
        htmlFor="ci-searchdomain"
      >
        <Input
          id="ci-searchdomain"
          value={searchdomain}
          onChange={(e) => {
            setSearchdomain(e.target.value);
          }}
          placeholder="example.com"
        />
      </SettingRow>
      <SettingRow
        label="SSH public keys"
        description="One key per line. They are added to the user's authorized keys."
        htmlFor="ci-sshkeys"
        className="sm:grid-cols-1"
      >
        <textarea
          id="ci-sshkeys"
          value={sshkeys}
          onChange={(e) => {
            setSshkeys(e.target.value);
          }}
          placeholder="ssh-rsa AAAA... user@host"
          rows={4}
          className="flex w-full rounded-md border border-input bg-transparent px-3 py-2 font-mono text-[12.5px] placeholder:text-muted-foreground focus-visible:outline-hidden focus-visible:ring-1 focus-visible:ring-ring"
        />
      </SettingRow>

      <SettingsFooter>
        {setConfigMutation.isError && (
          <p className="mr-auto text-sm text-destructive">
            {setConfigMutation.error.message}
          </p>
        )}
        {setConfigMutation.isSuccess && (
          <p className="mr-auto text-sm text-muted-foreground">
            Cloud-init configuration saved.
          </p>
        )}
        <Button
          className="gap-2"
          onClick={handleSave}
          disabled={setConfigMutation.isPending}
        >
          <Save className="h-4 w-4" />
          {setConfigMutation.isPending ? "Saving..." : "Save"}
        </Button>
      </SettingsFooter>
    </SettingsSection>
  );
}
