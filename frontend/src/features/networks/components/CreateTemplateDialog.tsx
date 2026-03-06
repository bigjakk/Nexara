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

export function CreateTemplateDialog() {
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [rules, setRules] = useState<FirewallRuleRequest[]>([]);

  const create = useCreateFirewallTemplate();

  const addRule = () => {
    setRules([
      ...rules,
      { type: "in", action: "ACCEPT", enable: 1 },
    ]);
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
          <Plus className="mr-1 h-4 w-4" />
          Create Template
        </Button>
      </DialogTrigger>
      <DialogContent className="max-w-2xl">
        <DialogHeader>
          <DialogTitle>Create Firewall Template</DialogTitle>
        </DialogHeader>
        <div className="space-y-4">
          <div className="space-y-2">
            <Label>Name</Label>
            <Input
              placeholder="e.g. Web Server Rules"
              value={name}
              onChange={(e) => { setName(e.target.value); }}
            />
          </div>
          <div className="space-y-2">
            <Label>Description</Label>
            <Input
              placeholder="Optional description"
              value={description}
              onChange={(e) => { setDescription(e.target.value); }}
            />
          </div>

          <div className="space-y-2">
            <div className="flex items-center justify-between">
              <Label>Rules</Label>
              <Button variant="outline" size="sm" onClick={addRule}>
                <Plus className="mr-1 h-3 w-3" />
                Add Rule
              </Button>
            </div>
            {rules.length === 0 ? (
              <p className="text-sm text-muted-foreground">
                No rules added yet. Click "Add Rule" to start.
              </p>
            ) : (
              <div className="space-y-2">
                {rules.map((rule, i) => (
                  <div
                    key={i}
                    className="flex items-center gap-2 rounded-md border p-2"
                  >
                    <Select
                      value={rule.type}
                      onValueChange={(v) => { updateRule(i, "type", v); }}
                    >
                      <SelectTrigger className="w-20">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem value="in">IN</SelectItem>
                        <SelectItem value="out">OUT</SelectItem>
                      </SelectContent>
                    </Select>
                    <Select
                      value={rule.action}
                      onValueChange={(v) => { updateRule(i, "action", v); }}
                    >
                      <SelectTrigger className="w-24">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem value="ACCEPT">ACCEPT</SelectItem>
                        <SelectItem value="DROP">DROP</SelectItem>
                        <SelectItem value="REJECT">REJECT</SelectItem>
                      </SelectContent>
                    </Select>
                    <Input
                      className="w-24"
                      placeholder="Proto"
                      value={rule.proto || ""}
                      onChange={(e) => { updateRule(i, "proto", e.target.value); }}
                    />
                    <Input
                      className="flex-1"
                      placeholder="D.Port"
                      value={rule.dport || ""}
                      onChange={(e) => { updateRule(i, "dport", e.target.value); }}
                    />
                    <Input
                      className="flex-1"
                      placeholder="Comment"
                      value={rule.comment || ""}
                      onChange={(e) => { updateRule(i, "comment", e.target.value); }}
                    />
                    <Button
                      variant="ghost"
                      size="icon"
                      onClick={() => { removeRule(i); }}
                    >
                      <Trash2 className="h-4 w-4 text-destructive" />
                    </Button>
                  </div>
                ))}
              </div>
            )}
          </div>

          <div className="flex justify-end gap-2">
            <Button variant="outline" onClick={() => { setOpen(false); }}>
              Cancel
            </Button>
            <Button
              onClick={handleSubmit}
              disabled={!name || create.isPending}
            >
              Create Template
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}
