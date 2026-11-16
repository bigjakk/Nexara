import { useId, useRef } from "react";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { useTranslation } from "react-i18next";

const TEMPLATE_VARS = [
  "{{.RuleName}}",
  "{{.Severity}}",
  "{{.ResourceName}}",
  "{{.NodeName}}",
  "{{.Metric}}",
  "{{.CurrentValue}}",
  "{{.Threshold}}",
  "{{.Operator}}",
  "{{.FiredAt}}",
  "{{.State}}",
  "{{.ClusterID}}",
  "{{.EscalationLevel}}",
  "{{.Message}}",
];

interface TemplateEditorProps {
  value: string;
  onChange: (value: string) => void;
  label?: string;
}

export function TemplateEditor({
  value,
  onChange,
  label,
}: TemplateEditorProps) {
  const { t } = useTranslation("alerts");
  const shownLabel = label ?? t("templateEditor.messageTemplateOptional");
  const id = useId();
  const textareaRef = useRef<HTMLTextAreaElement>(null);

  const insertVariable = (variable: string) => {
    const textarea = textareaRef.current;
    if (!textarea) {
      onChange(value + variable);
      return;
    }

    const start = textarea.selectionStart;
    const end = textarea.selectionEnd;
    const newValue = value.slice(0, start) + variable + value.slice(end);
    onChange(newValue);

    // Restore cursor position after the inserted variable.
    requestAnimationFrame(() => {
      textarea.focus();
      const pos = start + variable.length;
      textarea.setSelectionRange(pos, pos);
    });
  };

  return (
    <div className="space-y-2">
      <Label htmlFor={id}>{shownLabel}</Label>
      <Textarea
        id={id}
        ref={textareaRef}
        value={value}
        onChange={(e) => {
          onChange(e.target.value);
        }}
        placeholder={t("templateEditor.customNotificationMessagePickA")}
        rows={3}
      />
      <div
        role="group"
        aria-label={t("templateEditor.insertAVariable")}
        className="flex flex-wrap gap-1"
      >
        {TEMPLATE_VARS.map((v) => (
          <button
            key={v}
            type="button"
            className="rounded-[5px] bg-foreground/[0.07] px-1.5 py-0.5 font-mono text-[11.5px] text-muted-foreground outline-hidden transition-colors hover:bg-foreground/[0.12] hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring"
            onClick={() => {
              insertVariable(v);
            }}
          >
            {v}
          </button>
        ))}
      </div>
      <p className="text-[12.5px] text-muted-foreground">
        {t("templateEditor.usesGoTemplateSyntaxLeave")}
      </p>
    </div>
  );
}
