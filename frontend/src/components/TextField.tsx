// TextField (ux-spec §9.3, 38px). onInput fires per keystroke; onCommit fires on blur and on Enter (single-line only).
import { useEffect, useRef } from "preact/hooks";
import type { TextFieldProps } from "./props";

export function TextField({
  id,
  label,
  value,
  onInput,
  onCommit,
  placeholder,
  error,
  hint,
  multiline,
  mono,
  type,
  autoFocus,
  maxLength,
}: TextFieldProps) {
  const node = useRef<HTMLInputElement | HTMLTextAreaElement | null>(null);
  useEffect(() => {
    if (autoFocus) node.current?.focus();
  }, [autoFocus]);

  const setNode = (el: HTMLInputElement | HTMLTextAreaElement | null) => {
    node.current = el;
  };
  const hintId = `${id}-hint`;
  const errId = `${id}-error`;
  const describedBy = [hint ? hintId : "", error ? errId : ""].filter(Boolean).join(" ") || undefined;
  const invalid = error ? "true" : undefined;
  const cls = mono ? "tf-control tf-mono" : "tf-control";

  return (
    <div class="tf">
      <label class="tf-label" htmlFor={id}>
        {label}
      </label>
      {multiline ? (
        <textarea
          ref={setNode}
          id={id}
          class={cls}
          rows={3}
          value={value}
          placeholder={placeholder}
          maxLength={maxLength}
          aria-invalid={invalid}
          aria-describedby={describedBy}
          onInput={(e) => onInput(e.currentTarget.value)}
          onBlur={(e) => onCommit?.(e.currentTarget.value)}
        />
      ) : (
        <input
          ref={setNode}
          id={id}
          type={type ?? "text"}
          class={cls}
          value={value}
          placeholder={placeholder}
          maxLength={maxLength}
          aria-invalid={invalid}
          aria-describedby={describedBy}
          onInput={(e) => onInput(e.currentTarget.value)}
          onBlur={(e) => onCommit?.(e.currentTarget.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") onCommit?.(e.currentTarget.value);
          }}
        />
      )}
      {hint ? (
        <p id={hintId} class="tf-hint">
          {hint}
        </p>
      ) : null}
      {error ? (
        <p id={errId} class="tf-error">
          {error}
        </p>
      ) : null}
    </div>
  );
}
