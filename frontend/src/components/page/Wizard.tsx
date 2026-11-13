import { useEffect, useRef, type ReactNode } from "react";
import { Check } from "lucide-react";
import { cn } from "@/lib/utils";
import { NativeSelect } from "./Field";

export interface WizardStep<Id extends string = string> {
  id: Id;
  label: string;
  /** One sentence under the step's heading: what this step decides. */
  hint?: string | undefined;
}

interface WizardProps<Id extends string> {
  steps: readonly WizardStep<Id>[];
  current: Id;
  /** A step in the list was pressed. */
  onStepChange: (id: Id) => void;
  /**
   * Whether the list may jump to a step: every step, unless given. A wizard
   * whose Next is gated passes the same rule here, so the list cannot be used
   * to skip a step Next would have held.
   */
  canGoTo?: ((id: Id) => boolean) | undefined;
  /** Stays above the footer whatever the scroll position: a submit error. */
  notice?: ReactNode;
  /** Cancel, Back, Next and the final action. Only one of them is primary. */
  footer: ReactNode;
  /** The current step's fields. */
  children: ReactNode;
  className?: string;
}

/**
 * The body of a multi-step dialog: the steps down the left, the current
 * step's fields beside them, the buttons along the foot. Below the `sm` width
 * the list folds into "Step 2 of 6:" and a picker of the steps over the fields.
 *
 * It fills its parent and scrolls its own fields, so the dialog around it is
 * a column with a height and no padding of its own:
 * `<DialogContent className="flex h-[640px] max-h-[85vh] flex-col gap-0 overflow-hidden p-0">`.
 */
export function Wizard<Id extends string>({
  steps,
  current,
  onStepChange,
  canGoTo,
  notice,
  footer,
  children,
  className,
}: WizardProps<Id>) {
  const index = Math.max(
    0,
    steps.findIndex((s) => s.id === current),
  );
  const step = steps[index];
  const fields = useRef<HTMLDivElement>(null);
  const heading = useRef<HTMLHeadingElement>(null);

  // A new step starts at its top, not where the last one was scrolled to, and
  // takes focus on its heading: the control that brought it up may be gone by
  // now (the Review step's Edit buttons leave with their step). Not on mount,
  // where the dialog places focus itself.
  const shown = useRef(current);
  useEffect(() => {
    if (shown.current === current) return;
    shown.current = current;
    if (fields.current) fields.current.scrollTop = 0;
    heading.current?.focus();
  }, [current]);

  return (
    <div className={cn("flex min-h-0 flex-1 flex-col", className)}>
      <div className="flex min-h-0 flex-1">
        <nav
          aria-label="Steps"
          className="hidden w-[188px] shrink-0 overflow-y-auto border-r px-2.5 py-4 sm:block"
        >
          <ol className="space-y-0.5">
            {steps.map((s, i) => {
              const state =
                i === index ? "current" : i < index ? "done" : "todo";
              return (
                <li key={s.id}>
                  <button
                    type="button"
                    aria-current={state === "current" ? "step" : undefined}
                    disabled={canGoTo !== undefined && !canGoTo(s.id)}
                    onClick={() => {
                      if (s.id !== current) onStepChange(s.id);
                    }}
                    className={cn(
                      "flex w-full items-center gap-2.5 rounded-md px-2 py-1.5 text-left text-sm outline-hidden transition-colors focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-default disabled:opacity-60",
                      state === "current"
                        ? "bg-primary/10 font-semibold text-foreground"
                        : "enabled:hover:bg-foreground/[0.06]",
                      state === "done" && "text-foreground",
                      state === "todo" &&
                        "text-muted-foreground enabled:hover:text-foreground",
                    )}
                  >
                    <span
                      aria-hidden="true"
                      className={cn(
                        "grid h-[22px] w-[22px] shrink-0 place-items-center rounded-full text-[11.5px] font-semibold",
                        state === "current" &&
                          "bg-primary text-primary-foreground",
                        state === "done" && "bg-primary/15 text-primary",
                        state === "todo" &&
                          "border border-input text-muted-foreground",
                      )}
                    >
                      {state === "done" ? (
                        <Check className="h-3 w-3" strokeWidth={3} />
                      ) : (
                        i + 1
                      )}
                    </span>
                    <span className="min-w-0 truncate">{s.label}</span>
                    {state === "done" && (
                      <span className="sr-only">, done</span>
                    )}
                  </button>
                </li>
              );
            })}
          </ol>
        </nav>
        <div
          ref={fields}
          className="min-h-0 min-w-0 flex-1 overflow-y-auto px-5 py-5 sm:px-6"
        >
          {step !== undefined && (
            <div className="mb-[18px]">
              <div className="flex items-center gap-2">
                <h3
                  ref={heading}
                  tabIndex={-1}
                  className="shrink-0 text-[15px] font-semibold leading-tight outline-hidden"
                >
                  <span className="font-normal text-muted-foreground sm:hidden">
                    Step {index + 1} of {steps.length}:{" "}
                  </span>
                  {/* Below `sm` the picker beside it shows the step's name. */}
                  <span className="max-sm:sr-only">{step.label}</span>
                </h3>
                {/* The step list's stand-in where there is no room for it,
                    held to the same rule about which steps may be reached. */}
                <NativeSelect
                  aria-label="Go to step"
                  className="h-8 flex-1 font-semibold sm:hidden"
                  value={current}
                  onChange={(e) => {
                    const picked = steps.find((s) => s.id === e.target.value);
                    if (picked && picked.id !== current) {
                      onStepChange(picked.id);
                    }
                  }}
                >
                  {steps.map((s) => (
                    <option
                      key={s.id}
                      value={s.id}
                      disabled={canGoTo !== undefined && !canGoTo(s.id)}
                    >
                      {s.label}
                    </option>
                  ))}
                </NativeSelect>
              </div>
              {step.hint !== undefined && (
                <p className="mt-1 text-[12.5px] text-muted-foreground">
                  {step.hint}
                </p>
              )}
            </div>
          )}
          {children}
        </div>
      </div>
      {notice !== undefined && notice !== null && notice !== false && (
        <div className="border-t px-5 py-2.5 sm:px-6">{notice}</div>
      )}
      <div className="flex flex-wrap items-center justify-end gap-2 border-t px-5 py-3.5 sm:px-6">
        {footer}
      </div>
    </div>
  );
}
