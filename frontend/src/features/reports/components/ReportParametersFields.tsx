import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import type { ReportParameters } from "@/types/api";
import { reportTypeInfo, type ReportParamKey } from "../report-types";

const PARAM_FIELDS: Record<
  ReportParamKey,
  { label: string; hint: string; min: number; max: number; placeholder: string }
> = {
  stale_after_hours: {
    label: "Stale after (hours)",
    hint: "A guest whose newest restore point is older than this is stale. Default 24, matching the coverage page.",
    min: 1,
    max: 8760,
    placeholder: "24",
  },
  top_n: {
    label: "Top consumers per table",
    hint: "How many guests each top-consumers table lists. Default 10.",
    min: 1,
    max: 100,
    placeholder: "10",
  },
  snapshot_warn_days: {
    label: "Flag snapshots older than (days)",
    hint: "Default 7.",
    min: 1,
    max: 3650,
    placeholder: "7",
  },
};

/** A copy of the parameters without one key; blank means the server default. */
function withoutKey(value: ReportParameters, key: string): ReportParameters {
  return Object.fromEntries(Object.entries(value).filter(([k]) => k !== key));
}

interface ReportParametersFieldsProps {
  reportType: string;
  value: ReportParameters;
  onChange: (next: ReportParameters) => void;
}

/**
 * The per-type options a run or schedule may set. Blank means the server
 * default, so the fields never invent a value the user did not type.
 */
export function ReportParametersFields({
  reportType,
  value,
  onChange,
}: ReportParametersFieldsProps) {
  const info = reportTypeInfo(reportType);
  if (
    !info ||
    (info.params.length === 0 && info.optionalSections.length === 0)
  ) {
    return null;
  }

  const setNumber = (key: ReportParamKey, raw: string) => {
    const n = Number(raw);
    if (raw === "" || !Number.isFinite(n) || n <= 0) {
      onChange(withoutKey(value, key));
      return;
    }
    onChange({ ...value, [key]: Math.floor(n) });
  };

  const setSection = (key: string, enabled: boolean) => {
    const sections: Record<string, boolean> = enabled
      ? Object.fromEntries(
          Object.entries(value.sections ?? {}).filter(([k]) => k !== key),
        )
      : { ...(value.sections ?? {}), [key]: false };
    if (Object.keys(sections).length === 0) {
      onChange(withoutKey(value, "sections"));
      return;
    }
    onChange({ ...value, sections });
  };

  return (
    <div className="space-y-3 rounded-md border p-3">
      <p className="text-xs font-medium text-muted-foreground">
        Report options
      </p>
      {info.params.map((key) => {
        const f = PARAM_FIELDS[key];
        const id = `report-param-${key}`;
        return (
          <div key={key} className="space-y-1">
            <Label htmlFor={id}>{f.label}</Label>
            <Input
              id={id}
              type="number"
              min={f.min}
              max={f.max}
              placeholder={f.placeholder}
              value={value[key] ?? ""}
              onChange={(e) => {
                setNumber(key, e.target.value);
              }}
            />
            <p className="text-xs text-muted-foreground">{f.hint}</p>
          </div>
        );
      })}
      {info.optionalSections.map((s) => {
        const id = `report-section-${s.key}`;
        const enabled = value.sections?.[s.key] !== false;
        return (
          <div key={s.key} className="flex items-center gap-2">
            <Switch
              id={id}
              checked={enabled}
              onCheckedChange={(on) => {
                setSection(s.key, on);
              }}
            />
            <Label htmlFor={id}>Include {s.label.toLowerCase()}</Label>
          </div>
        );
      })}
    </div>
  );
}
