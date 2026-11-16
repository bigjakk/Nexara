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
import { useSaveOutcome } from "@/hooks/useSaveOutcome";
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
  const settle = useSaveOutcome(open);
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
    // The dialog stays mounted while closed, so a create that lands after it
    // closed clears the form too.
    const clear = () => {
      setProto("");
      setSource("");
      setDest("");
      setDport("");
      setComment("");
      setGroup("");
    };
    settle(create.mutateAsync(req), {
      action: t("createFirewallRuleAction"),
      onSuccess: () => {
        setOpen(false);
        clear();
      },
      onLateSuccess: (_result, now) => {
        if (!now.open) clear();
      },
      onError: () => undefined, // shown below from the mutation's state
    });
  };

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button size="sm">
          <Plus className="h-4 w-4" />
          {t("createFirewallRuleDialog.addRule")}
        </Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            {t("createFirewallRuleDialog.addFirewallRule")}
          </DialogTitle>
        </DialogHeader>
        <div className="space-y-4">
          <div className="grid grid-cols-2 gap-4">
            <div className="space-y-2">
              <Label>{t("createFirewallRuleDialog.direction")}</Label>
              <Select value={type} onValueChange={setType}>
                <SelectTrigger
                  aria-label={t("createFirewallRuleDialog.direction")}
                >
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="in">
                    {t("createFirewallRuleDialog.in")}
                  </SelectItem>
                  <SelectItem value="out">
                    {t("createFirewallRuleDialog.out")}
                  </SelectItem>
                  <SelectItem value="group">
                    {t("createFirewallRuleDialog.group")}
                  </SelectItem>
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
                <Label>{t("createFirewallRuleDialog.action")}</Label>
                <Select value={action} onValueChange={setAction}>
                  <SelectTrigger>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="ACCEPT">
                      {t("createFirewallRuleDialog.accept")}
                    </SelectItem>
                    <SelectItem value="DROP">
                      {t("createFirewallRuleDialog.drop")}
                    </SelectItem>
                    <SelectItem value="REJECT">
                      {t("createFirewallRuleDialog.reject")}
                    </SelectItem>
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
                <Label>
                  {dport.length > 0
                    ? t("createFirewallRuleDialog.protocol")
                    : t("createFirewallRuleDialog.protocolOptional")}
                </Label>
                <Select value={proto} onValueChange={setProto}>
                  <SelectTrigger
                    className={needsProto ? "border-destructive" : ""}
                  >
                    <SelectValue
                      placeholder={t("createFirewallRuleDialog.any")}
                    />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="tcp">
                      {t("createFirewallRuleDialog.tcp")}
                    </SelectItem>
                    <SelectItem value="udp">
                      {t("createFirewallRuleDialog.udp")}
                    </SelectItem>
                    <SelectItem value="icmp">
                      {t("createFirewallRuleDialog.icmp")}
                    </SelectItem>
                  </SelectContent>
                </Select>
                {needsProto && (
                  <p className="text-xs text-destructive">
                    {t("createFirewallRuleDialog.protocolIsRequiredWhenA")}
                  </p>
                )}
              </div>
              <div className="grid grid-cols-2 gap-4">
                <div className="space-y-2">
                  <Label>{t("createFirewallRuleDialog.sourceOptional")}</Label>
                  <Input
                    placeholder={t(
                      "createFirewallRuleDialog.sourcePlaceholder",
                    )}
                    value={source}
                    onChange={(e) => {
                      setSource(e.target.value);
                    }}
                  />
                </div>
                <div className="space-y-2">
                  <Label>
                    {t("createFirewallRuleDialog.destinationOptional")}
                  </Label>
                  <Input
                    placeholder={t(
                      "createFirewallRuleDialog.destinationPlaceholder",
                    )}
                    value={dest}
                    onChange={(e) => {
                      setDest(e.target.value);
                    }}
                  />
                </div>
              </div>
              <div className="space-y-2">
                <Label>
                  {t("createFirewallRuleDialog.destinationPortOptional")}
                </Label>
                <Input
                  placeholder={t(
                    "createFirewallRuleDialog.destinationPortPlaceholder",
                  )}
                  value={dport}
                  onChange={(e) => {
                    setDport(e.target.value);
                  }}
                />
              </div>
            </>
          )}
          <div className="space-y-2">
            <Label>{t("createFirewallRuleDialog.commentOptional")}</Label>
            <Input
              placeholder={t("createFirewallRuleDialog.ruleDescription")}
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
            <Label htmlFor="enable-rule">
              {t("createFirewallRuleDialog.enableRule")}
            </Label>
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
              {t("createFirewallRuleDialog.cancel")}
            </Button>
            <Button
              onClick={handleSubmit}
              disabled={create.isPending || (isGroup && group === "")}
            >
              {t("createFirewallRuleDialog.create")}
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}
