// FROZEN CONTRACT (implemented in Wave 0). Every rejected facade call becomes an AppError (architecture §3.4).
export type ErrorCode =
  | "validation" | "not_found" | "conflict" | "auth_missing" | "auth_invalid" | "forbidden" | "offline" | "timeout"
  | "rate_limited" | "source_http" | "filter_invalid" | "keychain_unavailable" | "sink_failed" | "unsupported"
  | "not_implemented" | "db" | "cancelled" | "internal";

export class AppError extends Error {
  readonly code: ErrorCode;
  readonly detail: string;
  readonly retryable: boolean;
  readonly field: string;
  constructor(code: ErrorCode, message: string, detail = "", retryable = false, field = "") {
    super(message);
    this.name = "AppError";
    this.code = code;
    this.detail = detail;
    this.retryable = retryable;
    this.field = field;
  }
}

const CODES = new Set<string>([
  "validation", "not_found", "conflict", "auth_missing", "auth_invalid", "forbidden", "offline", "timeout",
  "rate_limited", "source_http", "filter_invalid", "keychain_unavailable", "sink_failed", "unsupported",
  "not_implemented", "db", "cancelled", "internal",
]);

/** Converts anything thrown by a Wails call (RuntimeError with .cause JSON, CancelError, …) into an AppError. */
export function toAppError(e: unknown): AppError {
  if (e instanceof AppError) return e;
  const err = e as { name?: string; message?: string; cause?: unknown } | null | undefined;
  if (err?.name === "CancelError" || err?.name === "AbortError") return new AppError("cancelled", "Cancelled.");
  let cause = err?.cause;
  if (typeof cause === "string") {
    try { cause = JSON.parse(cause); } catch { cause = undefined; }
  }
  if (cause && typeof cause === "object") {
    const c = cause as Record<string, unknown>;
    const code = typeof c.code === "string" && CODES.has(c.code) ? (c.code as ErrorCode) : "internal";
    return new AppError(
      code,
      typeof c.message === "string" && c.message ? c.message : "Something went wrong.",
      typeof c.detail === "string" ? c.detail : "",
      c.retryable === true,
      typeof c.field === "string" ? c.field : "",
    );
  }
  return new AppError("internal", "Something went wrong.", err?.message ?? String(e));
}

/** Wraps a promise so it rejects with AppError. */
export async function wrap<T>(p: PromiseLike<T>): Promise<T> {
  try {
    return await p;
  } catch (e) {
    throw toAppError(e);
  }
}
