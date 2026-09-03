import { useEffect, useRef, useState } from "react";
import { Clock, TriangleAlert } from "lucide-react";
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
import { cn } from "@/lib/utils";
import {
  WEEKDAYS,
  describeSchedule,
  formatSchedule,
  ordinal,
  parseSchedule,
  type ScheduleFrequency,
  type ScheduleSpec,
} from "../lib/schedule";

const FREQUENCIES: { value: ScheduleFrequency; label: string }[] = [
  { value: "hourly", label: "Hourly" },
  { value: "daily", label: "Daily" },
  { value: "weekly", label: "Weekly" },
  { value: "monthly", label: "Monthly" },
  { value: "custom", label: "Custom" },
];

const EVERY_HOURS = [2, 3, 4, 6, 8, 12];
const HOURS = Array.from({ length: 24 }, (_, i) => i);
const MINUTES = Array.from({ length: 12 }, (_, i) => i * 5);
const DAYS_OF_MONTH = Array.from({ length: 31 }, (_, i) => i + 1);

function pad(n: number): string {
  return String(n).padStart(2, "0");
}

interface ScheduleBuilderProps {
  /** The PVE calendar string, e.g. "02:00" or "mon,fri 22:30". */
  value: string;
  onChange: (value: string) => void;
}

/**
 * Builds a PVE backup schedule from presets instead of asking for systemd
 * calendar syntax, with a Custom mode that still accepts the raw string.
 */
export function ScheduleBuilder({ value, onChange }: ScheduleBuilderProps) {
  const [spec, setSpec] = useState<ScheduleSpec>(() => parseSchedule(value));
  // Tracks what this component last pushed up, so a value changed elsewhere
  // (opening the dialog on another job, resetting after create) re-seeds the
  // controls while the user's own edits don't round-trip back through parse.
  const emitted = useRef(value);

  useEffect(() => {
    if (value !== emitted.current) {
      emitted.current = value;
      setSpec(parseSchedule(value));
    }
  }, [value]);

  function update(patch: Partial<ScheduleSpec>) {
    const next = { ...spec, ...patch };
    setSpec(next);
    const formatted = formatSchedule(next);
    emitted.current = formatted;
    onChange(formatted);
  }

  function selectFrequency(frequency: ScheduleFrequency) {
    if (frequency === spec.frequency) return;
    if (frequency === "custom") {
      // Carry the built schedule over so Custom starts from what's on screen.
      update({ frequency, custom: formatSchedule(spec) });
      return;
    }
    update({ frequency });
  }

  function toggleWeekday(day: string) {
    const next = spec.weekdays.includes(day)
      ? spec.weekdays.filter((d) => d !== day)
      : [...spec.weekdays, day];
    update({ weekdays: next });
  }

  // Values a hand-written schedule can hold that aren't in the presets stay
  // selectable, rather than leaving the control blank or snapping them.
  const minutes = MINUTES.includes(spec.minute)
    ? MINUTES
    : [...MINUTES, spec.minute].sort((a, b) => a - b);
  const everyHours =
    spec.everyHours === 1 || EVERY_HOURS.includes(spec.everyHours)
      ? EVERY_HOURS
      : [...EVERY_HOURS, spec.everyHours].sort((a, b) => a - b);

  const timeFields = (
    <div className="space-y-2">
      <Label>Time</Label>
      <div className="flex items-center gap-2">
        <Select
          value={String(spec.hour)}
          onValueChange={(v) => {
            update({ hour: Number(v) });
          }}
        >
          <SelectTrigger className="w-20" aria-label="Hour">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {HOURS.map((h) => (
              <SelectItem key={h} value={String(h)}>
                {pad(h)}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <span className="text-muted-foreground">:</span>
        <Select
          value={String(spec.minute)}
          onValueChange={(v) => {
            update({ minute: Number(v) });
          }}
        >
          <SelectTrigger className="w-20" aria-label="Minute">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {minutes.map((m) => (
              <SelectItem key={m} value={String(m)}>
                {pad(m)}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>
    </div>
  );

  const description = describeSchedule(value);
  const invalid = value.trim() === "";

  return (
    <div className="space-y-3 rounded-md border p-3">
      <div className="flex flex-wrap gap-1">
        {FREQUENCIES.map((f) => (
          <Button
            key={f.value}
            type="button"
            size="sm"
            variant={spec.frequency === f.value ? "secondary" : "ghost"}
            aria-pressed={spec.frequency === f.value}
            className={cn(
              "h-7 px-3 text-xs font-normal",
              spec.frequency === f.value && "font-medium",
            )}
            onClick={() => {
              selectFrequency(f.value);
            }}
          >
            {f.label}
          </Button>
        ))}
      </div>

      {spec.frequency === "hourly" && (
        <div className="grid grid-cols-2 gap-4">
          <div className="space-y-2">
            <Label>Every</Label>
            <Select
              value={String(spec.everyHours)}
              onValueChange={(v) => {
                update({ everyHours: Number(v) });
              }}
            >
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="1">Hour</SelectItem>
                {everyHours.map((h) => (
                  <SelectItem key={h} value={String(h)}>
                    {String(h)} hours
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="space-y-2">
            <Label>At minute</Label>
            <Select
              value={String(spec.minute)}
              onValueChange={(v) => {
                update({ minute: Number(v) });
              }}
            >
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {minutes.map((m) => (
                  <SelectItem key={m} value={String(m)}>
                    :{pad(m)}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        </div>
      )}

      {spec.frequency === "daily" && timeFields}

      {spec.frequency === "weekly" && (
        <div className="space-y-3">
          <div className="space-y-2">
            <Label>Days</Label>
            <div className="flex flex-wrap gap-1">
              {WEEKDAYS.map((day) => {
                const active = spec.weekdays.includes(day.value);
                return (
                  <Button
                    key={day.value}
                    type="button"
                    size="sm"
                    variant={active ? "default" : "outline"}
                    aria-pressed={active}
                    className="h-7 w-12 px-0 text-xs font-normal"
                    onClick={() => {
                      toggleWeekday(day.value);
                    }}
                  >
                    {day.short}
                  </Button>
                );
              })}
            </div>
          </div>
          {timeFields}
        </div>
      )}

      {spec.frequency === "monthly" && (
        <div className="space-y-3">
          <div className="space-y-2">
            <Label>Day of month</Label>
            <Select
              value={String(spec.dayOfMonth)}
              onValueChange={(v) => {
                update({ dayOfMonth: Number(v) });
              }}
            >
              <SelectTrigger className="w-28">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {DAYS_OF_MONTH.map((d) => (
                  <SelectItem key={d} value={String(d)}>
                    {String(d)}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            {spec.dayOfMonth > 28 && (
              <p className="text-xs text-muted-foreground">
                Months without a {ordinal(spec.dayOfMonth)} are skipped.
              </p>
            )}
          </div>
          {timeFields}
        </div>
      )}

      {spec.frequency === "custom" && (
        <div className="space-y-2">
          <Label htmlFor="schedule-custom">Calendar event</Label>
          <Input
            id="schedule-custom"
            value={spec.custom}
            onChange={(e) => {
              update({ custom: e.target.value });
            }}
            placeholder="mon..fri 02:00"
            className="font-mono"
          />
          <p className="text-xs text-muted-foreground">
            systemd calendar syntax, e.g.{" "}
            <span className="font-mono">mon..fri 02:00</span>,{" "}
            <span className="font-mono">*-*-1,15 04:00</span>,{" "}
            <span className="font-mono">*/4:30</span>.
          </p>
        </div>
      )}

      <div
        className={cn(
          "flex items-center gap-2 rounded-md bg-muted/50 px-3 py-2 text-sm",
          invalid && "text-destructive",
        )}
      >
        {invalid ? (
          <TriangleAlert className="h-4 w-4 shrink-0" />
        ) : (
          <Clock className="h-4 w-4 shrink-0 text-muted-foreground" />
        )}
        <span className="min-w-0 truncate">
          {invalid
            ? spec.frequency === "weekly"
              ? "Pick at least one day"
              : "Enter a schedule"
            : (description ?? "Custom schedule")}
        </span>
        {!invalid && (
          <code className="ml-auto shrink-0 rounded bg-background px-1.5 py-0.5 font-mono text-xs text-muted-foreground">
            {value}
          </code>
        )}
      </div>
    </div>
  );
}
