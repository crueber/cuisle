// Segmented: a radiogroup (ux-spec §9.3) with roving selection.
// Arrow keys move focus and select (APG radio group), wrapping and skipping disabled options.
// With `toggle` it is a view switch instead (the board's Assignee | Status): a labelled group of aria-pressed buttons.
import { useRef } from "preact/hooks";
import type { SegmentedProps } from "./props";

export function Segmented<V extends string>({ id, label, value, options, onChange, toggle }: SegmentedProps<V>) {
  const refs = useRef<(HTMLButtonElement | null)[]>([]);

  function move(from: number, dir: 1 | -1) {
    const n = options.length;
    for (let step = 1; step < n; step++) {
      const i = (((from + dir * step) % n) + n) % n;
      const opt = options[i];
      if (opt && !opt.disabled) {
        refs.current[i]?.focus();
        onChange(opt.value);
        return;
      }
    }
  }

  if (toggle) {
    return (
      <div id={id} class="seg" role="group" aria-label={label}>
        {options.map((opt) => (
          <button
            key={opt.value}
            type="button"
            class="seg-option"
            aria-pressed={opt.value === value ? "true" : "false"}
            disabled={opt.disabled}
            onClick={() => {
              if (!opt.disabled && opt.value !== value) onChange(opt.value);
            }}
          >
            {opt.label}
          </button>
        ))}
      </div>
    );
  }

  return (
    <div id={id} class="seg" role="radiogroup" aria-label={label}>
      {options.map((opt, i) => {
        const checked = opt.value === value;
        return (
          <button
            key={opt.value}
            ref={(el) => {
              refs.current[i] = el;
            }}
            type="button"
            role="radio"
            class="seg-option"
            aria-checked={checked ? "true" : "false"}
            tabIndex={checked ? 0 : -1}
            disabled={opt.disabled}
            onClick={() => {
              if (!opt.disabled && !checked) onChange(opt.value);
            }}
            onKeyDown={(e) => {
              if (e.key === "ArrowRight" || e.key === "ArrowDown") {
                e.preventDefault();
                move(i, 1);
              } else if (e.key === "ArrowLeft" || e.key === "ArrowUp") {
                e.preventDefault();
                move(i, -1);
              }
            }}
          >
            {opt.label}
          </button>
        );
      })}
    </div>
  );
}
