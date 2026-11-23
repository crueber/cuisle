// Stepper: "− value +" (ux-spec §9.3). Changes are clamped to [min, max]; a no-op never calls onChange.
import type { StepperProps } from "./props";

export function Stepper({ id, label, value, min, max, step = 1, suffix, zeroLabel, onChange }: StepperProps) {
  const shown = value === 0 && zeroLabel ? zeroLabel : `${value}${suffix ?? ""}`;

  const set = (next: number) => {
    const clamped = Math.min(max, Math.max(min, next));
    if (clamped !== value) onChange(clamped);
  };

  return (
    <div id={id} class="stepper">
      <button type="button" class="stepper-btn" aria-label="Decrease" disabled={value <= min} onClick={() => set(value - step)}>
        −
      </button>
      <span
        role="spinbutton"
        tabIndex={0}
        class="stepper-value"
        aria-label={label}
        aria-valuenow={value}
        aria-valuemin={min}
        aria-valuemax={max}
        aria-valuetext={shown}
        onKeyDown={(e) => {
          if (e.key === "ArrowUp") {
            e.preventDefault();
            set(value + step);
          } else if (e.key === "ArrowDown") {
            e.preventDefault();
            set(value - step);
          }
        }}
      >
        {shown}
      </span>
      <button type="button" class="stepper-btn" aria-label="Increase" disabled={value >= max} onClick={() => set(value + step)}>
        +
      </button>
    </div>
  );
}
