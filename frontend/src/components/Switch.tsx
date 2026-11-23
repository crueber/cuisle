// Switch: role=switch (ux-spec §9.3, 44x24). Disabled switches never call onChange.
import type { SwitchProps } from "./props";

export function Switch({ id, label, checked, onChange, disabled, description }: SwitchProps) {
  const labelId = `${id}-label`;
  const descId = `${id}-desc`;
  return (
    <div class="switch-row">
      <div class="switch-text">
        <span id={labelId} class="switch-label">
          {label}
        </span>
        {description ? (
          <span id={descId} class="switch-desc">
            {description}
          </span>
        ) : null}
      </div>
      <button
        id={id}
        type="button"
        role="switch"
        class="switch"
        aria-checked={checked ? "true" : "false"}
        aria-labelledby={labelId}
        aria-describedby={description ? descId : undefined}
        disabled={disabled}
        onClick={() => {
          if (!disabled) onChange(!checked);
        }}
      >
        <span class="switch-knob" aria-hidden="true" />
      </button>
    </div>
  );
}
