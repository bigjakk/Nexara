import { describe, it, expect } from "vitest";
import {
  DEFAULT_SCHEDULE,
  describeSchedule,
  formatSchedule,
  parseSchedule,
} from "./schedule";

describe("formatSchedule", () => {
  it("formats each frequency", () => {
    expect(
      formatSchedule({ ...DEFAULT_SCHEDULE, frequency: "daily", hour: 2, minute: 30 }),
    ).toBe("02:30");
    expect(
      formatSchedule({ ...DEFAULT_SCHEDULE, frequency: "hourly", everyHours: 6, minute: 0 }),
    ).toBe("*/6:00");
    expect(
      formatSchedule({ ...DEFAULT_SCHEDULE, frequency: "hourly", everyHours: 1, minute: 15 }),
    ).toBe("*:15");
    expect(
      formatSchedule({
        ...DEFAULT_SCHEDULE,
        frequency: "weekly",
        weekdays: ["fri", "mon"],
        hour: 22,
        minute: 0,
      }),
    ).toBe("mon,fri 22:00");
    expect(
      formatSchedule({
        ...DEFAULT_SCHEDULE,
        frequency: "monthly",
        dayOfMonth: 1,
        hour: 4,
        minute: 5,
      }),
    ).toBe("*-*-01 04:05");
  });

  it("passes custom strings through and rejects an empty weekday set", () => {
    expect(
      formatSchedule({ ...DEFAULT_SCHEDULE, frequency: "custom", custom: " mon..fri 09:00 " }),
    ).toBe("mon..fri 09:00");
    expect(
      formatSchedule({ ...DEFAULT_SCHEDULE, frequency: "weekly", weekdays: [] }),
    ).toBe("");
  });
});

describe("parseSchedule", () => {
  it("round-trips the strings the builder emits", () => {
    for (const raw of ["02:30", "*/6:00", "*:15", "mon,fri 22:00", "*-*-01 04:05"]) {
      expect(formatSchedule(parseSchedule(raw))).toBe(raw);
    }
  });

  it("reads systemd shorthands", () => {
    expect(parseSchedule("hourly")).toMatchObject({ frequency: "hourly", everyHours: 1 });
    expect(parseSchedule("daily")).toMatchObject({ frequency: "daily", hour: 0, minute: 0 });
    expect(parseSchedule("weekly")).toMatchObject({ frequency: "weekly", weekdays: ["mon"] });
    expect(parseSchedule("monthly")).toMatchObject({ frequency: "monthly", dayOfMonth: 1 });
  });

  it("expands weekday ranges", () => {
    expect(parseSchedule("mon..fri 09:00")).toMatchObject({
      frequency: "weekly",
      weekdays: ["mon", "tue", "wed", "thu", "fri"],
      hour: 9,
    });
    expect(parseSchedule("sat,sun 03:00")).toMatchObject({
      frequency: "weekly",
      weekdays: ["sat", "sun"],
    });
  });

  it("keeps anything it cannot model as custom", () => {
    for (const raw of ["*-*-01/2 02:00", "yearly", "mon 25:00", "12:60", "sun..mon 02:00"]) {
      expect(parseSchedule(raw)).toMatchObject({ frequency: "custom", custom: raw });
    }
  });

  it("falls back to the default spec for an empty schedule", () => {
    expect(parseSchedule("")).toEqual(DEFAULT_SCHEDULE);
    expect(parseSchedule(undefined)).toEqual(DEFAULT_SCHEDULE);
  });
});

describe("describeSchedule", () => {
  it("renders plain English", () => {
    expect(describeSchedule("02:30")).toBe("Every day at 02:30");
    expect(describeSchedule("*/6:00")).toBe("Every 6 hours at :00");
    expect(describeSchedule("*:00")).toBe("Every hour, on the hour");
    expect(describeSchedule("*:30")).toBe("Every hour at :30");
    expect(describeSchedule("sun 03:00")).toBe("Every Sunday at 03:00");
    expect(describeSchedule("mon,wed,fri 22:00")).toBe("Every Mon, Wed and Fri at 22:00");
    expect(describeSchedule("mon..sun 01:00")).toBe("Every day at 01:00");
    expect(describeSchedule("*-*-02 04:05")).toBe("On the 2nd of each month at 04:05");
    expect(describeSchedule("*-*-21 04:05")).toBe("On the 21st of each month at 04:05");
  });

  it("returns null when it has nothing better than the raw string", () => {
    expect(describeSchedule("*-*-01/2 02:00")).toBeNull();
    expect(describeSchedule("")).toBeNull();
    expect(describeSchedule(undefined)).toBeNull();
  });
});
