import { useRef } from "react";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { Badge } from "@/components/ui/badge";

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
];

interface TemplateEditorProps {
  value: string;
  onChange: (value: string) => void;
  label?: string;
}

export function TemplateEditor({
  value,
  onChange,
  label = "Message Template (optional)",
}: TemplateEditorProps) {
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
      <Label>{label}</Label>
      <div className="flex flex-wrap gap-1 mb-1">
        {TEMPLATE_VARS.map((v) => (
          <Badge
            key={v}
            variant="secondary"
            className="cursor-pointer hover:bg-muted-foreground/20 text-xs"
            onClick={() => {
              insertVariable(v);
            }}
          >
            {v}
          </Badge>
        ))}
      </div>
      <Textarea
        ref={textareaRef}
        value={value}
        onChange={(e) => {
          onChange(e.target.value);
        }}
        placeholder="Custom notification message. Click variables above to insert."
        rows={3}
      />
      <p className="text-xs text-muted-foreground">
        Uses Go template syntax. Leave empty for default message.
      </p>
    </div>
  );
}
