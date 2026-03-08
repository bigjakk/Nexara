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
import { Plus, Trash2 } from "lucide-react";
import { useNotificationChannels } from "../api/alert-queries";
import type { EscalationStep } from "@/types/api";

interface EscalationChainEditorProps {
  steps: EscalationStep[];
  onChange: (steps: EscalationStep[]) => void;
}

export function EscalationChainEditor({
  steps,
  onChange,
}: EscalationChainEditorProps) {
  const { data: channels } = useNotificationChannels();

  const addStep = () => {
    onChange([...steps, { channel_id: "", delay_minutes: 0 }]);
  };

  const removeStep = (index: number) => {
    onChange(steps.filter((_, i) => i !== index));
  };

  const updateStep = (
    index: number,
    field: keyof EscalationStep,
    value: string | number,
  ) => {
    const updated = steps.map((step, i) => {
      if (i !== index) return step;
      return { ...step, [field]: value };
    });
    onChange(updated);
  };

  return (
    <div className="space-y-3">
      <Label>Escalation Chain</Label>
      {steps.length === 0 && (
        <p className="text-sm text-muted-foreground">
          No escalation steps. Add one to send notifications when alerts fire.
        </p>
      )}
      {steps.map((step, index) => (
        <div key={index} className="flex items-center gap-2">
          <span className="text-sm text-muted-foreground w-6">
            {index + 1}.
          </span>
          <Select
            value={step.channel_id}
            onValueChange={(v) => {
              updateStep(index, "channel_id", v);
            }}
          >
            <SelectTrigger className="flex-1">
              <SelectValue placeholder="Select channel" />
            </SelectTrigger>
            <SelectContent>
              {channels?.map((ch) => (
                <SelectItem key={ch.id} value={ch.id}>
                  {ch.name} ({ch.channel_type})
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <div className="flex items-center gap-1">
            <Input
              type="number"
              min="0"
              className="w-20"
              value={step.delay_minutes}
              onChange={(e) => {
                updateStep(index, "delay_minutes", Number(e.target.value));
              }}
            />
            <span className="text-xs text-muted-foreground whitespace-nowrap">
              min delay
            </span>
          </div>
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={() => {
              removeStep(index);
            }}
          >
            <Trash2 className="h-4 w-4 text-destructive" />
          </Button>
        </div>
      ))}
      <Button type="button" variant="outline" size="sm" onClick={addStep}>
        <Plus className="mr-1 h-3 w-3" />
        Add Step
      </Button>
    </div>
  );
}
