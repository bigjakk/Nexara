import { useState } from "react";
import { Check, ChevronsUpDown } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from "@/components/ui/command";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { cn } from "@/lib/utils";
import {
  WEEKDAY_LABELS,
  listTimezones,
  type ParsedSchedule,
  type ScheduleMode,
} from "../lib/virtio-win-schedule";

interface TimezoneFieldProps {
  value: string;
  disabled: boolean;
  onChange: (tz: string) => void;
}

/**
 * Searchable IANA zone picker. A plain Select would be several hundred items
 * deep, and the zone matters enough to be worth typing at: left on the server's
 * own zone, "03:00" in a container means 03:00 UTC.
 */
function TimezoneField({ value, disabled, onChange }: TimezoneFieldProps) {
  const [open, setOpen] = useState(false);
  const zones = listTimezones();

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button
          type="button"
          variant="outline"
          role="combobox"
          aria-expanded={open}
          disabled={disabled}
          className="w-full justify-between font-normal"
        >
          {value === "" ? "Server time" : value}
          <ChevronsUpDown className="ml-2 h-4 w-4 shrink-0 opacity-50" />
        </Button>
      </PopoverTrigger>
      <PopoverContent className="w-[--radix-popover-trigger-width] p-0">
        <Command>
          <CommandInput placeholder="Search time zones..." />
          <CommandList>
            <CommandEmpty>No matching time zone</CommandEmpty>
            <CommandGroup>
              <CommandItem
                value="Server time"
                onSelect={() => {
                  onChange("");
                  setOpen(false);
                }}
              >
                <Check
                  className={cn(
                    "mr-2 h-4 w-4",
                    value === "" ? "opacity-100" : "opacity-0",
                  )}
                />
                Server time
              </CommandItem>
              {zones.map((zone) => (
                <CommandItem
                  key={zone}
                  value={zone}
                  onSelect={() => {
                    onChange(zone);
                    setOpen(false);
                  }}
                >
                  <Check
                    className={cn(
                      "mr-2 h-4 w-4",
                      value === zone ? "opacity-100" : "opacity-0",
                    )}
                  />
                  {zone}
                </CommandItem>
              ))}
            </CommandGroup>
          </CommandList>
        </Command>
      </PopoverContent>
    </Popover>
  );
}

interface VirtioWinScheduleFieldsProps {
  schedule: ParsedSchedule;
  timezone: string;
  disabled: boolean;
  onScheduleChange: (next: ParsedSchedule) => void;
  onTimezoneChange: (tz: string) => void;
}

/**
 * The "when does this check" half of the config card.
 *
 * What is being scheduled is not really the upstream lookup — that is one small
 * request — but the ~840 MB fetch a lookup can dispatch, which is why an
 * operator wants it aimed at a window rather than at whenever the container
 * last restarted.
 */
export function VirtioWinScheduleFields({
  schedule,
  timezone,
  disabled,
  onScheduleChange,
  onTimezoneChange,
}: VirtioWinScheduleFieldsProps) {
  const timed = schedule.mode === "daily" || schedule.mode === "weekly";

  return (
    <div className="space-y-3">
      <div className="space-y-2">
        <Label htmlFor="virtio-win-schedule-mode">Check schedule</Label>
        <div className="flex flex-wrap items-center gap-2">
          <Select
            value={schedule.mode}
            disabled={disabled}
            onValueChange={(v) => {
              onScheduleChange({ ...schedule, mode: v as ScheduleMode });
            }}
          >
            <SelectTrigger id="virtio-win-schedule-mode" className="w-56">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="interval">Every 6 hours</SelectItem>
              <SelectItem value="daily">Daily at</SelectItem>
              <SelectItem value="weekly">Weekly on</SelectItem>
              {/* Offered only when that is what is already stored: the card
                  writes the other three, and a cron field is not something to
                  put in front of someone who did not ask for it. */}
              {schedule.mode === "custom" && (
                <SelectItem value="custom">Custom (cron)</SelectItem>
              )}
            </SelectContent>
          </Select>

          {schedule.mode === "weekly" && (
            <Select
              value={String(schedule.weekday)}
              disabled={disabled}
              onValueChange={(v) => {
                onScheduleChange({ ...schedule, weekday: Number(v) });
              }}
            >
              <SelectTrigger className="w-36" aria-label="Day of week">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {WEEKDAY_LABELS.map((label, index) => (
                  <SelectItem key={label} value={String(index)}>
                    {label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          )}

          {timed && (
            <Input
              type="time"
              aria-label="Time of day"
              className="w-32"
              value={schedule.time}
              disabled={disabled}
              onChange={(e) => {
                onScheduleChange({ ...schedule, time: e.target.value });
              }}
            />
          )}

          {schedule.mode === "custom" && (
            <Input
              aria-label="Cron expression"
              className="w-56 font-mono"
              placeholder="0 3 * * *"
              value={schedule.cron}
              disabled={disabled}
              onChange={(e) => {
                onScheduleChange({ ...schedule, cron: e.target.value });
              }}
            />
          )}
        </div>
      </div>

      {schedule.mode === "interval" ? (
        <p className="text-xs text-muted-foreground">
          Counted from the last check, so a restart no longer resets the cycle.
        </p>
      ) : (
        <div className="space-y-2">
          <Label>Time zone</Label>
          <TimezoneField
            value={timezone}
            disabled={disabled}
            onChange={onTimezoneChange}
          />
          <p className="text-xs text-muted-foreground">
            Left on server time, the hour above is read in the container&apos;s
            own zone &mdash; almost always UTC.
          </p>
        </div>
      )}
    </div>
  );
}
