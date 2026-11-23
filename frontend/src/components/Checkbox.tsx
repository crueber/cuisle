// Checkbox: a native checkbox (24px), labelled by its text. Disabled checkboxes never call onChange.
import type { CheckboxProps } from "./props";

export function Checkbox({ id, label, checked, onChange, disabled }: CheckboxProps) {
  return (
    <label class="checkbox" htmlFor={id}>
      <input
        id={id}
        type="checkbox"
        class="checkbox-input"
        checked={checked}
        disabled={disabled}
        onChange={() => {
          if (!disabled) onChange(!checked);
        }}
      />
      <span class="checkbox-box" aria-hidden="true" />
      <span class="checkbox-label">{label}</span>
    </label>
  );
}
