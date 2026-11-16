import { useId } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
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
import { useTranslation } from "react-i18next";

interface EscalationChainEditorProps {
  steps: EscalationStep[];
  onChange: (steps: EscalationStep[]) => void;
}

export function EscalationChainEditor({
  steps,
  onChange,
}: EscalationChainEditorProps) {
  const { t } = useTranslation("alerts");
  const { data: channels } = useNotificationChannels();
  const labelId = useId();

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
    <div role="group" aria-labelledby={labelId} className="space-y-2.5">
      <div id={labelId} className="text-sm font-medium leading-none">
        {t("escalationChainEditor.escalationChain")}
      </div>
      {steps.length === 0 && (
        <p className="text-sm text-muted-foreground">
          {t("escalationChainEditor.noEscalationStepsAddOne")}
        </p>
      )}
      {steps.map((step, index) => (
        <div key={index} className="flex flex-wrap items-center gap-2">
          <span className="w-5 text-sm text-muted-foreground">
            {index + 1}.
          </span>
          <Select
            value={step.channel_id}
            onValueChange={(v) => {
              updateStep(index, "channel_id", v);
            }}
          >
            <SelectTrigger
              className="min-w-0 flex-[1_1_10rem]"
              aria-label={t("escalationChainEditor.channelForStepIndex", {
                index: String(index + 1),
              })}
            >
              <SelectValue
                placeholder={t("escalationChainEditor.selectChannel")}
              />
            </SelectTrigger>
            <SelectContent>
              {channels?.map((ch) => (
                <SelectItem key={ch.id} value={ch.id}>
                  {ch.name} ({ch.channel_type})
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <div className="flex items-center gap-1.5">
            <Input
              type="number"
              min="0"
              className="w-20"
              aria-label={t("escalationChainEditor.delayInMinutesForStep", {
                index: String(index + 1),
              })}
              value={step.delay_minutes}
              onChange={(e) => {
                updateStep(index, "delay_minutes", Number(e.target.value));
              }}
            />
            <span className="whitespace-nowrap text-[12.5px] text-muted-foreground">
              {t("escalationChainEditor.minDelay")}
            </span>
          </div>
          <Button
            aria-label={t("escalationChainEditor.removeEscalationStepIndex", {
              index: String(index + 1),
            })}
            type="button"
            variant="ghost"
            size="sm"
            className="h-8 px-2 text-muted-foreground hover:text-red-700 dark:hover:text-red-400"
            onClick={() => {
              removeStep(index);
            }}
          >
            <Trash2 className="h-4 w-4" />
          </Button>
        </div>
      ))}
      <Button
        type="button"
        variant="outline"
        size="sm"
        className="gap-1"
        onClick={addStep}
      >
        <Plus className="h-3 w-3" />
        {t("escalationChainEditor.addStep")}
      </Button>
    </div>
  );
}
