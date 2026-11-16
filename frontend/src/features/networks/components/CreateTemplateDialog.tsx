import { useState } from "react";
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
import { Plus, Trash2 } from "lucide-react";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { useCreateFirewallTemplate } from "../api/network-queries";
import type { FirewallRuleRequest } from "../types/network";
import { useTranslation } from "react-i18next";

export function CreateTemplateDialog() {
  const { t } = useTranslation("networks");
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [rules, setRules] = useState<FirewallRuleRequest[]>([]);

  const create = useCreateFirewallTemplate();

  const addRule = () => {
    setRules([...rules, { type: "in", action: "ACCEPT", enable: 1 }]);
  };

  const removeRule = (index: number) => {
    setRules(rules.filter((_, i) => i !== index));
  };

  const updateRule = (index: number, field: string, value: string | number) => {
    setRules(
      rules.map((rule, i) =>
        i === index ? { ...rule, [field]: value } : rule,
      ),
    );
  };

  const handleSubmit = () => {
    create.mutate(
      { name, description, rules },
      {
        onSuccess: () => {
          setOpen(false);
          setName("");
          setDescription("");
          setRules([]);
        },
      },
    );
  };

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button size="sm">
          <Plus className="h-4 w-4" />
          {t("createTemplateDialog.createTemplate")}
        </Button>
      </DialogTrigger>
      <DialogContent className="max-w-2xl">
        <DialogHeader>
          <DialogTitle>
            {t("createTemplateDialog.createFirewallTemplate")}
          </DialogTitle>
        </DialogHeader>
        <div className="space-y-4">
          <div className="space-y-2">
            <Label>{t("createTemplateDialog.name")}</Label>
            <Input
              placeholder={t("createTemplateDialog.eGWebServerRules")}
              value={name}
              onChange={(e) => {
                setName(e.target.value);
              }}
            />
          </div>
          <div className="space-y-2">
            <Label>{t("createTemplateDialog.description")}</Label>
            <Input
              placeholder={t("createTemplateDialog.optionalDescription")}
              value={description}
              onChange={(e) => {
                setDescription(e.target.value);
              }}
            />
          </div>

          <div className="space-y-2">
            <div className="flex items-center justify-between">
              <Label>{t("createTemplateDialog.rules")}</Label>
              <Button variant="outline" size="sm" onClick={addRule}>
                <Plus className="h-3 w-3" />
                {t("createTemplateDialog.addRule")}
              </Button>
            </div>
            {rules.length === 0 ? (
              <p className="text-sm text-muted-foreground">
                {t("createTemplateDialog.noRulesAddedYetClick")}
              </p>
            ) : (
              <div className="space-y-2">
                {rules.map((rule, i) => (
                  <div
                    key={i}
                    className="panel flex flex-wrap items-center gap-2 rounded-[8px] p-2"
                  >
                    <Select
                      value={rule.type}
                      onValueChange={(v) => {
                        updateRule(i, "type", v);
                      }}
                    >
                      <SelectTrigger className="w-20">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem value="in">
                          {t("createTemplateDialog.in")}
                        </SelectItem>
                        <SelectItem value="out">
                          {t("createTemplateDialog.out")}
                        </SelectItem>
                      </SelectContent>
                    </Select>
                    <Select
                      value={rule.action}
                      onValueChange={(v) => {
                        updateRule(i, "action", v);
                      }}
                    >
                      <SelectTrigger className="w-24">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem value="ACCEPT">
                          {t("createTemplateDialog.accept")}
                        </SelectItem>
                        <SelectItem value="DROP">
                          {t("createTemplateDialog.drop")}
                        </SelectItem>
                        <SelectItem value="REJECT">
                          {t("createTemplateDialog.reject")}
                        </SelectItem>
                      </SelectContent>
                    </Select>
                    <Input
                      className="w-24"
                      placeholder={t("createTemplateDialog.proto")}
                      value={rule.proto || ""}
                      onChange={(e) => {
                        updateRule(i, "proto", e.target.value);
                      }}
                    />
                    <Input
                      className="flex-1"
                      placeholder={t("createTemplateDialog.dPort")}
                      value={rule.dport || ""}
                      onChange={(e) => {
                        updateRule(i, "dport", e.target.value);
                      }}
                    />
                    <Input
                      className="flex-1"
                      placeholder={t("createTemplateDialog.comment")}
                      value={rule.comment || ""}
                      onChange={(e) => {
                        updateRule(i, "comment", e.target.value);
                      }}
                    />
                    <Button
                      aria-label={t("createTemplateDialog.removeRuleI", {
                        i: String(i + 1),
                      })}
                      variant="ghost"
                      size="icon"
                      className="h-8 w-8 text-muted-foreground hover:text-destructive"
                      onClick={() => {
                        removeRule(i);
                      }}
                    >
                      <Trash2 className="h-4 w-4" />
                    </Button>
                  </div>
                ))}
              </div>
            )}
          </div>

          <div className="flex justify-end gap-2">
            <Button
              variant="outline"
              onClick={() => {
                setOpen(false);
              }}
            >
              {t("createTemplateDialog.cancel")}
            </Button>
            <Button onClick={handleSubmit} disabled={!name || create.isPending}>
              {t("createTemplateDialog.createTemplate")}
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}
