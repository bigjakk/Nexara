/**
 * The instants a From/To pair of date inputs (YYYY-MM-DD) stands for: the whole
 * of both days in the browser's time zone. Both ends are read as local time; a
 * bare "YYYY-MM-DD" is read as UTC midnight, which shifted From by the
 * browser's offset while To stayed local.
 */
export function dayWindow(
  from: string,
  to: string,
): { startTime: string | undefined; endTime: string | undefined } {
  return {
    startTime: from ? new Date(`${from}T00:00:00`).toISOString() : undefined,
    endTime: to ? new Date(`${to}T23:59:59`).toISOString() : undefined,
  };
}
