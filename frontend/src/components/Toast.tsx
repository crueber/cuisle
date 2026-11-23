// Toast: a status message with an optional action and a dismiss control. No timers: the caller unmounts it.
import type { ToastProps } from "./props";

export function Toast({ message, action, onDismiss }: ToastProps) {
  return (
    <div class="toast" role="status">
      <span class="toast-message">{message}</span>
      {action ? (
        <button type="button" class="toast-action" onClick={() => action.run()}>
          {action.label}
        </button>
      ) : null}
      <button type="button" class="toast-dismiss" aria-label="Dismiss" onClick={() => onDismiss()}>
        <span aria-hidden="true">×</span>
      </button>
    </div>
  );
}
