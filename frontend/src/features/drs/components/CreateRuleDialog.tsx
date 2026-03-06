import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { useCreateDRSRule } from "../api/drs-queries";
import type { RuleType } from "../types/drs";
import { Plus } from "lucide-react";

interface CreateRuleDialogProps {
  clusterId: string;
}

export function CreateRuleDialog({ clusterId }: CreateRuleDialogProps) {
  const [open, setOpen] = useState(false);
  const [ruleType, setRuleType] = useState<RuleType>("affinity");
  const [vmIds, setVmIds] = useState("");
  const [nodeNames, setNodeNames] = useState("");
  const [enabled, setEnabled] = useState(true);

  const createRule = useCreateDRSRule(clusterId);

  const handleSubmit = () => {
    const parsedVmIds = vmIds
      .split(",")
      .map((s) => parseInt(s.trim(), 10))
      .filter((n) => !isNaN(n));

    const parsedNodes = nodeNames
      .split(",")
      .map((s) => s.trim())
      .filter((s) => s.length > 0);

    createRule.mutate(
      {
        rule_type: ruleType,
        vm_ids: parsedVmIds,
        node_names: parsedNodes,
        enabled,
      },
      {
        onSuccess: () => {
          setOpen(false);
          setVmIds("");
          setNodeNames("");
          setRuleType("affinity");
          setEnabled(true);
        },
      },
    );
  };

  const parsedVmIds = vmIds
    .split(",")
    .map((s) => parseInt(s.trim(), 10))
    .filter((n) => !isNaN(n));

  const isValid = parsedVmIds.length >= 2 || ruleType === "pin";

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button size="sm">
          <Plus className="mr-2 h-4 w-4" />
          Add Rule
        </Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Create DRS Rule</DialogTitle>
        </DialogHeader>

        <div className="space-y-4">
          <div className="space-y-2">
            <Label>Rule Type</Label>
            <Select
              value={ruleType}
              onValueChange={(v) => setRuleType(v as RuleType)}
            >
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="affinity">Affinity</SelectItem>
                <SelectItem value="anti-affinity">Anti-Affinity</SelectItem>
                <SelectItem value="pin">Pin</SelectItem>
              </SelectContent>
            </Select>
          </div>

          <div className="space-y-2">
            <Label htmlFor="vm-ids">VM IDs (comma-separated)</Label>
            <Input
              id="vm-ids"
              placeholder="100, 101, 102"
              value={vmIds}
              onChange={(e) => setVmIds(e.target.value)}
            />
          </div>

          {ruleType === "pin" && (
            <div className="space-y-2">
              <Label htmlFor="node-names">Node Names (comma-separated)</Label>
              <Input
                id="node-names"
                placeholder="node1, node2"
                value={nodeNames}
                onChange={(e) => setNodeNames(e.target.value)}
              />
            </div>
          )}

          <div className="flex items-center gap-2">
            <Checkbox
              id="rule-enabled"
              checked={enabled}
              onCheckedChange={(checked) => setEnabled(checked === true)}
            />
            <Label htmlFor="rule-enabled">Enabled</Label>
          </div>
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={() => setOpen(false)}>
            Cancel
          </Button>
          <Button
            onClick={handleSubmit}
            disabled={createRule.isPending || !isValid}
          >
            {createRule.isPending ? "Creating..." : "Create Rule"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
