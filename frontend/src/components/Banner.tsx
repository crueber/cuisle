// Banner: a persistent notice per kind. The caller supplies the copy; colour is never the only signal (the message is text).
import type { BannerProps } from "./props";

const ASSERTIVE: BannerProps["kind"][] = ["offline", "auth_invalid", "error"];

export function Banner({ kind, message, action, onDismiss }: BannerProps) {
  return (
    <div class="banner" data-kind={kind} role={ASSERTIVE.includes(kind) ? "alert" : "status"}>
      <span class="banner-message">{message}</span>
      {action ? (
        <button type="button" class="banner-action" onClick={() => action.run()}>
          {action.label}
        </button>
      ) : null}
      {onDismiss ? (
        <button type="button" class="banner-dismiss" aria-label="Dismiss" onClick={() => onDismiss()}>
          <span aria-hidden="true">×</span>
        </button>
      ) : null}
    </div>
  );
}
