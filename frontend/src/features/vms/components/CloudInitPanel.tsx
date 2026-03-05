import { useEffect, useState } from "react";
import { Loader2, Save } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
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
    return (
      <div className="flex items-center justify-center py-8">
        <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
      </div>
    );
  }

  if (!hasCloudInit) {
    return (
      <div className="rounded-lg border p-6 text-center">
        <p className="text-sm text-muted-foreground">
          No cloud-init drive detected. Add a cloud-init drive (IDE, SCSI, etc.)
          to this VM to configure cloud-init settings.
        </p>
      </div>
    );
  }

  return (
    <div className="space-y-4">
      <div className="grid gap-4 sm:grid-cols-2">
        <div className="space-y-2">
          <Label htmlFor="ci-user">User</Label>
          <Input
            id="ci-user"
            value={ciuser}
            onChange={(e) => {
              setCiuser(e.target.value);
            }}
            placeholder="root"
          />
        </div>
        <div className="space-y-2">
          <Label htmlFor="ci-password">Password</Label>
          <Input
            id="ci-password"
            type="password"
            value={cipassword}
            onChange={(e) => {
              setCipassword(e.target.value);
            }}
            placeholder="Leave blank to keep current"
          />
        </div>
        <div className="space-y-2 sm:col-span-2">
          <Label htmlFor="ci-ipconfig">IP Config (ipconfig0)</Label>
          <Input
            id="ci-ipconfig"
            value={ipconfig0}
            onChange={(e) => {
              setIpconfig0(e.target.value);
            }}
            placeholder="ip=dhcp or ip=192.168.1.100/24,gw=192.168.1.1"
          />
        </div>
        <div className="space-y-2">
          <Label htmlFor="ci-nameserver">DNS Server</Label>
          <Input
            id="ci-nameserver"
            value={nameserver}
            onChange={(e) => {
              setNameserver(e.target.value);
            }}
            placeholder="8.8.8.8"
          />
        </div>
        <div className="space-y-2">
          <Label htmlFor="ci-searchdomain">Search Domain</Label>
          <Input
            id="ci-searchdomain"
            value={searchdomain}
            onChange={(e) => {
              setSearchdomain(e.target.value);
            }}
            placeholder="example.com"
          />
        </div>
        <div className="space-y-2 sm:col-span-2">
          <Label htmlFor="ci-sshkeys">SSH Public Keys</Label>
          <textarea
            id="ci-sshkeys"
            value={sshkeys}
            onChange={(e) => {
              setSshkeys(e.target.value);
            }}
            placeholder="ssh-rsa AAAA... user@host"
            rows={3}
            className="flex w-full rounded-md border border-input bg-transparent px-3 py-2 text-sm shadow-sm placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
          />
        </div>
      </div>

      {setConfigMutation.isError && (
        <p className="text-sm text-destructive">
          {setConfigMutation.error.message}
        </p>
      )}
      {setConfigMutation.isSuccess && (
        <p className="text-sm text-green-600 dark:text-green-500">
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
    </div>
  );
}
