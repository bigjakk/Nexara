import { useState } from "react";
import { useTranslation } from "react-i18next";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Plus } from "lucide-react";
import { useCreateClusterFirewallRule } from "../api/network-queries";
import { useFirewallSecurityGroups } from "../api/firewall-extra-queries";
import type { FirewallRuleRequest } from "../types/network";

interface CreateFirewallRuleDialogProps {
  clusterId: string;
}

export function CreateFirewallRuleDialog({
  clusterId,
}: CreateFirewallRuleDialogProps) {
  const [open, setOpen] = useState(false);
  const [type, setType] = useState("in");
  const [action, setAction] = useState("ACCEPT");
  const [proto, setProto] = useState("");
  const [source, setSource] = useState("");
  const [dest, setDest] = useState("");
  const [dport, setDport] = useState("");
  const [comment, setComment] = useState("");
  const [enable, setEnable] = useState(true);
  // A group rule names its security group in `action` and matches nothing of
  // its own (pve-firewall verify_rule and ruleset_add_group_rule).
  const [group, setGroup] = useState("");
  const isGroup = type === "group";
  const { t } = useTranslation("networks");
  const groups = useFirewallSecurityGroups(open && isGroup ? clusterId : "");

  const create = useCreateClusterFirewallRule(clusterId);
  const errorMessage =
    create.error instanceof Error ? create.error.message : "";

  const needsProto = !isGroup && dport.length > 0 && proto.length === 0;

  const handleSubmit = () => {
    if (needsProto || (isGroup && group === "")) return;
    const req: FirewallRuleRequest = {
      type,
      action: isGroup ? group : action,
      enable: enable ? 1 : 0,
    };
    if (!isGroup) {
      if (proto) req.proto = proto;
      if (source) req.source = source;
      if (dest) req.dest = dest;
      if (dport) req.dport = dport;
    }
    if (comment) req.comment = comment;
    create.mutate(req, {
      onSuccess: () => {
        setOpen(false);
        setProto("");
        setSource("");
        setDest("");
        setDport("");
        setComment("");
        setGroup("");
      },
    });
  };

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button size="sm">
          <Plus className="h-4 w-4" />
          Add Rule
        </Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Add firewall rule</DialogTitle>
        </DialogHeader>
        <div className="space-y-4">
          <div className="grid grid-cols-2 gap-4">
            <div className="space-y-2">
              <Label>Direction</Label>
              <Select value={type} onValueChange={setType}>
                <SelectTrigger aria-label="Direction">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="in">In</SelectItem>
                  <SelectItem value="out">Out</SelectItem>
                  <SelectItem value="group">Group</SelectItem>
                </SelectContent>
              </Select>
            </div>
            {isGroup ? (
              <div className="space-y-2">
                <Label>{t("securityGroup")}</Label>
                <Select value={group} onValueChange={setGroup}>
                  <SelectTrigger aria-label={t("securityGroup")}>
                    <SelectValue placeholder={t("chooseSecurityGroup")} />
                  </SelectTrigger>
                  <SelectContent>
                    {(groups.data ?? []).map((g) => (
                      <SelectItem key={g.group} value={g.group}>
                        {g.group}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
            ) : (
              <div className="space-y-2">
                <Label>Action</Label>
                <Select value={action} onValueChange={setAction}>
                  <SelectTrigger>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="ACCEPT">ACCEPT</SelectItem>
                    <SelectItem value="DROP">DROP</SelectItem>
                    <SelectItem value="REJECT">REJECT</SelectItem>
                  </SelectContent>
                </Select>
              </div>
            )}
          </div>
          {isGroup && groups.data?.length === 0 && (
            <p className="text-xs text-muted-foreground">
              {t("noSecurityGroups")}
            </p>
          )}
          {isGroup && groups.isError && (
            <p className="text-xs text-destructive">
              {t("failedLoadSecurityGroups")}
            </p>
          )}
          {!isGroup && (
            <>
              <div className="space-y-2">
                <Label>Protocol{dport.length > 0 ? "" : " (optional)"}</Label>
                <Select value={proto} onValueChange={setProto}>
                  <SelectTrigger
                    className={needsProto ? "border-destructive" : ""}
                  >
                    <SelectValue placeholder="Any" />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="tcp">TCP</SelectItem>
                    <SelectItem value="udp">UDP</SelectItem>
                    <SelectItem value="icmp">ICMP</SelectItem>
                  </SelectContent>
                </Select>
                {needsProto && (
                  <p className="text-xs text-destructive">
                    Protocol is required when a destination port is specified.
                  </p>
                )}
              </div>
              <div className="grid grid-cols-2 gap-4">
                <div className="space-y-2">
                  <Label>Source (optional)</Label>
                  <Input
                    placeholder="e.g. 10.0.0.0/24"
                    value={source}
                    onChange={(e) => {
                      setSource(e.target.value);
                    }}
                  />
                </div>
                <div className="space-y-2">
                  <Label>Destination (optional)</Label>
                  <Input
                    placeholder="e.g. 10.0.1.0/24"
                    value={dest}
                    onChange={(e) => {
                      setDest(e.target.value);
                    }}
                  />
                </div>
              </div>
              <div className="space-y-2">
                <Label>Destination port (optional)</Label>
                <Input
                  placeholder="e.g. 80, 443, 8000-9000"
                  value={dport}
                  onChange={(e) => {
                    setDport(e.target.value);
                  }}
                />
              </div>
            </>
          )}
          <div className="space-y-2">
            <Label>Comment (optional)</Label>
            <Input
              placeholder="Rule description"
              value={comment}
              onChange={(e) => {
                setComment(e.target.value);
              }}
            />
          </div>
          <div className="flex items-center gap-2">
            <input
              type="checkbox"
              id="enable-rule"
              checked={enable}
              onChange={(e) => {
                setEnable(e.target.checked);
              }}
              className="rounded border"
            />
            <Label htmlFor="enable-rule">Enable rule</Label>
          </div>
          {errorMessage && (
            <p className="text-sm text-destructive">{errorMessage}</p>
          )}
          <div className="flex justify-end gap-2">
            <Button
              variant="outline"
              onClick={() => {
                setOpen(false);
              }}
            >
              Cancel
            </Button>
            <Button
              onClick={handleSubmit}
              disabled={create.isPending || (isGroup && group === "")}
            >
              Create
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}
