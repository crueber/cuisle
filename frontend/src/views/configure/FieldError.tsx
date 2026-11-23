// The save queue's validation message for one setting (key `${standupId}:${field}`). TextFields take it as their
// `error` prop; Steppers have no such prop, so this renders the same message under them.
import { fieldErrors } from "../../state/save";

/** The validation message the last save of `${standupId}:${field}` was rejected with, if any. */
export function fieldError(standupId: number, field: string): string | undefined {
  return fieldErrors.value.get(`${standupId}:${field}`)?.message;
}

export function FieldError({ standupId, field }: { standupId: number; field: string }) {
  const message = fieldError(standupId, field);
  return message ? (
    <p class="tf-error" role="alert">
      {message}
    </p>
  ) : null;
}
