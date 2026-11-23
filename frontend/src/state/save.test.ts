// WP-15 tests: the autosave queue (state/save.ts; architecture §5.3, testing §4 "Also cover").
//
// SPEC-NOTE: "retry 3x (0.5 s, 1 s, 2 s)" is read as ONE initial run plus THREE retries, so four runs in total, at
//   t = 0, 500, 1500 and 3500 ms after the first failure, and then the key is `failed`.
// SPEC-NOTE: "Saving…" counts from when the bound call STARTS (for a debounced job that is when the debounce fires,
//   not when the user typed), because the spec says "a call is outstanding > 250 ms".
// SPEC-NOTE: "Saved" is shown from the moment the last outstanding call completes, for SAVED_MS, then `settled`.
// SPEC-NOTE: a retryable error is one with AppError.retryable === true (db, timeout in practice). A non-retryable,
//   non-validation error (e.g. source_http) fails at once with no retry.
// SPEC-NOTE: whether a `validation` error also puts the key in failedKeys / sets saveState "failed" is NOT asserted
//   (the spec says only "the key stays dirty"); only fieldErrors[key] and the absence of retries are.
// SPEC-NOTE: when a newer job arrives for a key whose older job is waiting out a retry backoff, the behaviour is not
//   asserted.
// SPEC-NOTE: pinned beyond the brief: flush() never rejects (not even when a job fails permanently); a later
//   successful save for a key clears that key's fieldErrors entry and its failedKeys entry; a newer IMMEDIATE job
//   replaces a still-pending DEBOUNCED job for the same key (last write wins); debounces of different keys are
//   independent; retryFailed re-runs the failed key's LAST job (one run per failed key) and is a no-op when nothing
//   failed; resetSaveQueue cancels pending debounces and returns every signal to its initial value.
// SPEC-NOTE: modules are re-imported fresh per test (vi.resetModules) because the queue has hidden module state.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

async function load() {
  vi.resetModules();
  const save = await import("./save");
  const { AppError } = await import("../api/errors");
  return { ...save, AppError };
}
type M = Awaited<ReturnType<typeof load>>;

function deferred<T = void>() {
  let resolve!: (v: T) => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<T>((a, b) => {
    resolve = a;
    reject = b;
  });
  return { promise, resolve, reject };
}
const tick = () => vi.advanceTimersByTimeAsync(0);
const advance = (ms: number) => vi.advanceTimersByTimeAsync(ms);
/** Attaches handlers at once so a stub rejection fails the assertion, not the process. */
function watch(p: Promise<unknown>) {
  const s = { settled: false, rejected: false };
  p.then(
    () => (s.settled = true),
    () => ((s.settled = true), (s.rejected = true)),
  );
  return s;
}

let S: M;
beforeEach(async () => {
  vi.useFakeTimers();
  S = await load();
});
afterEach(() => {
  vi.useRealTimers();
});

describe("enqueue: immediate jobs", () => {
  it("runs_an_immediate_job_without_waiting_for_a_timer", async () => {
    const run = vi.fn(async () => "ok");
    S.enqueue({ key: "3:pulseDays", run });
    await tick();
    expect(run).toHaveBeenCalledTimes(1);
  });

  it("passes_the_run_result_to_onSaved", async () => {
    const onSaved = vi.fn();
    S.enqueue({ key: "3:pulseDays", run: async () => ({ rev: 9 }), onSaved });
    await tick();
    expect(onSaved).toHaveBeenCalledWith({ rev: 9 });
  });

  it("does_not_call_onSaved_when_the_run_fails", async () => {
    const onSaved = vi.fn();
    S.enqueue({
      key: "3:pulseDays",
      run: async () => {
        throw new S.AppError("source_http", "no", "", false);
      },
      onSaved,
    });
    await advance(10_000);
    expect(onSaved).not.toHaveBeenCalled();
  });
});

describe("enqueue: text debounce (400 ms)", () => {
  it("waits_400ms_before_running_a_debounced_job", async () => {
    const run = vi.fn(async () => undefined);
    S.enqueue({ key: "3:name", run, debounceMs: 400 });
    await advance(399);
    expect(run).not.toHaveBeenCalled();
    await advance(1);
    expect(run).toHaveBeenCalledTimes(1);
  });

  it("restarts_the_debounce_on_each_keystroke_and_runs_only_the_newest_job", async () => {
    const first = vi.fn(async () => undefined);
    const second = vi.fn(async () => undefined);
    S.enqueue({ key: "3:name", run: first, debounceMs: 400 });
    await advance(300);
    S.enqueue({ key: "3:name", run: second, debounceMs: 400 });
    await advance(399); // t = 699
    expect(first).not.toHaveBeenCalled();
    expect(second).not.toHaveBeenCalled();
    await advance(1); // t = 700
    expect(second).toHaveBeenCalledTimes(1);
    await advance(5_000);
    expect(first).not.toHaveBeenCalled();
  });

  it("debounces_different_keys_independently", async () => {
    const a = vi.fn(async () => undefined);
    const b = vi.fn(async () => undefined);
    S.enqueue({ key: "3:name", run: a, debounceMs: 400 });
    await advance(200);
    S.enqueue({ key: "3:extraFilter", run: b, debounceMs: 400 });
    await advance(200); // t = 400
    expect(a).toHaveBeenCalledTimes(1);
    expect(b).not.toHaveBeenCalled();
    await advance(200); // t = 600
    expect(b).toHaveBeenCalledTimes(1);
  });

  it("lets_a_newer_immediate_job_replace_a_pending_debounced_one_for_the_same_key", async () => {
    const typed = vi.fn(async () => undefined);
    const committed = vi.fn(async () => undefined);
    S.enqueue({ key: "3:name", run: typed, debounceMs: 400 });
    S.enqueue({ key: "3:name", run: committed });
    await advance(5_000);
    expect(committed).toHaveBeenCalledTimes(1);
    expect(typed).not.toHaveBeenCalled();
  });
});

describe("flush", () => {
  it("fires_a_pending_debounce_immediately", async () => {
    const run = vi.fn(async () => undefined);
    S.enqueue({ key: "3:name", run, debounceMs: 400 });
    const p = watch(S.flush());
    await tick(); // no time has passed
    expect(run).toHaveBeenCalledTimes(1);
    expect(p.settled).toBe(true);
  });

  it("resolves_only_after_in_flight_and_queued_jobs_complete", async () => {
    const d1 = deferred();
    const d2 = deferred();
    const run1 = vi.fn(() => d1.promise);
    const run2 = vi.fn(() => d2.promise);
    S.enqueue({ key: "3:name", run: run1 });
    S.enqueue({ key: "3:name", run: run2 });
    const p = watch(S.flush());
    await tick();
    expect(p.settled).toBe(false);
    d1.resolve();
    await tick();
    expect(run2).toHaveBeenCalledTimes(1);
    expect(p.settled).toBe(false);
    d2.resolve();
    await tick();
    expect(p.settled).toBe(true);
    expect(p.rejected).toBe(false);
  });

  it("resolves_immediately_when_the_queue_is_idle", async () => {
    const p = watch(S.flush());
    await tick();
    expect(p.settled).toBe(true);
    expect(p.rejected).toBe(false);
  });

  it("does_not_reject_when_a_job_fails_permanently", async () => {
    S.enqueue({
      key: "3:name",
      run: async () => {
        throw new S.AppError("source_http", "no", "", false);
      },
    });
    await expect(S.flush()).resolves.toBeUndefined();
  });
});

describe("per-key serialisation and coalescing", () => {
  it("does_not_start_a_second_job_for_a_key_until_the_first_completes", async () => {
    const d1 = deferred();
    const run1 = vi.fn(() => d1.promise);
    const run2 = vi.fn(async () => undefined);
    S.enqueue({ key: "3:name", run: run1 });
    await tick();
    S.enqueue({ key: "3:name", run: run2 });
    await advance(1_000);
    expect(run1).toHaveBeenCalledTimes(1);
    expect(run2).not.toHaveBeenCalled();
    d1.resolve();
    await tick();
    expect(run2).toHaveBeenCalledTimes(1);
  });

  it("coalesces_pending_jobs_last_write_wins_so_only_the_newest_runs_after_the_in_flight_one", async () => {
    const d1 = deferred();
    const order: string[] = [];
    S.enqueue({
      key: "3:name",
      run: () => {
        order.push("v1");
        return d1.promise;
      },
    });
    await tick();
    S.enqueue({ key: "3:name", run: async () => void order.push("v2") });
    S.enqueue({ key: "3:name", run: async () => void order.push("v3") });
    d1.resolve();
    await advance(5_000);
    expect(order).toEqual(["v1", "v3"]);
  });

  it("runs_jobs_for_the_same_key_in_enqueue_order", async () => {
    const order: string[] = [];
    const d1 = deferred();
    S.enqueue({
      key: "3:pulseDays",
      run: async () => {
        order.push("a:start");
        await d1.promise;
        order.push("a:end");
      },
    });
    await tick();
    S.enqueue({ key: "3:pulseDays", run: async () => void order.push("b") });
    d1.resolve();
    await advance(10);
    expect(order).toEqual(["a:start", "a:end", "b"]);
  });

  it("does_not_make_a_job_for_one_key_wait_for_another_keys_in_flight_job", async () => {
    const slow = deferred();
    const slowRun = vi.fn(() => slow.promise);
    const fastRun = vi.fn(async () => undefined);
    S.enqueue({ key: "3:name", run: slowRun });
    await tick();
    S.enqueue({ key: "3:pulseDays", run: fastRun });
    await tick();
    expect(slowRun).toHaveBeenCalledTimes(1);
    expect(fastRun).toHaveBeenCalledTimes(1);
  });

  it("allows_a_later_key_to_finish_before_an_earlier_one_without_blocking", async () => {
    const slow = deferred();
    const done: string[] = [];
    S.enqueue({
      key: "3:name",
      run: async () => {
        await slow.promise;
        done.push("name");
      },
    });
    S.enqueue({ key: "3:rowsMode", run: async () => void done.push("rowsMode") });
    await tick();
    expect(done).toEqual(["rowsMode"]);
    slow.resolve();
    await tick();
    expect(done).toEqual(["rowsMode", "name"]);
  });
});

describe("saveState indicator", () => {
  it("goes_straight_to_saved_without_showing_saving_when_the_call_is_quick", async () => {
    expect(S.saveState.value).toBe("idle");
    const seen: string[] = [];
    S.saveState.subscribe((v) => seen.push(v));
    const d = deferred();
    S.enqueue({ key: "3:pulseDays", run: () => d.promise });
    await advance(100);
    d.resolve();
    await tick();
    expect(S.saveState.value).toBe("saved");
    expect(seen).not.toContain("saving");
  });

  it("shows_saving_only_after_a_call_has_been_outstanding_for_250ms", async () => {
    const d = deferred();
    S.enqueue({ key: "3:pulseDays", run: () => d.promise });
    await advance(249);
    expect(S.saveState.value).not.toBe("saving");
    await advance(1);
    expect(S.saveState.value).toBe("saving");
    d.resolve();
    await tick();
    expect(S.saveState.value).toBe("saved");
  });

  it("keeps_saved_for_2s_then_settles_to_all_changes_saved", async () => {
    S.enqueue({ key: "3:pulseDays", run: async () => undefined });
    await tick();
    expect(S.saveState.value).toBe("saved");
    await advance(1_999);
    expect(S.saveState.value).toBe("saved");
    await advance(1);
    expect(S.saveState.value).toBe("settled");
  });

  it("does_not_show_saving_while_a_debounced_job_is_still_waiting", async () => {
    S.enqueue({ key: "3:name", run: () => new Promise<void>(() => undefined), debounceMs: 400 });
    await advance(399);
    expect(S.saveState.value).toBe("idle");
  });

  it("counts_the_250ms_from_when_the_debounced_call_starts", async () => {
    S.enqueue({ key: "3:name", run: () => new Promise<void>(() => undefined), debounceMs: 400 });
    await advance(400 + 249);
    expect(S.saveState.value).not.toBe("saving");
    await advance(1);
    expect(S.saveState.value).toBe("saving");
  });
});

describe("failures and retries", () => {
  const retryable = () => new S.AppError("db", "Could not save.", "", true);

  it("retries_a_retryable_failure_three_times_at_0_5s_1s_2s_then_fails", async () => {
    const run = vi.fn(async () => {
      throw retryable();
    });
    S.enqueue({ key: "3:pulseDays", run });
    await tick();
    expect(run).toHaveBeenCalledTimes(1);
    await advance(499);
    expect(run).toHaveBeenCalledTimes(1);
    await advance(1); // t = 500
    expect(run).toHaveBeenCalledTimes(2);
    await advance(999); // t = 1499
    expect(run).toHaveBeenCalledTimes(2);
    await advance(1); // t = 1500
    expect(run).toHaveBeenCalledTimes(3);
    await advance(1_999); // t = 3499
    expect(run).toHaveBeenCalledTimes(3);
    await advance(1); // t = 3500
    expect(run).toHaveBeenCalledTimes(4);
    await advance(60_000);
    expect(run).toHaveBeenCalledTimes(4);
    expect(S.saveState.value).toBe("failed");
    expect([...S.failedKeys.value]).toEqual(["3:pulseDays"]);
  });

  it("recovers_when_a_retry_succeeds", async () => {
    const onSaved = vi.fn();
    const run = vi
      .fn<() => Promise<string>>()
      .mockRejectedValueOnce(retryable())
      .mockResolvedValueOnce("ok");
    S.enqueue({ key: "3:pulseDays", run, onSaved });
    await advance(500);
    expect(run).toHaveBeenCalledTimes(2);
    expect(onSaved).toHaveBeenCalledWith("ok");
    expect(S.failedKeys.value.size).toBe(0);
    expect(S.saveState.value).toBe("saved");
  });

  it("does_not_retry_a_non_retryable_error_and_marks_the_key_failed_at_once", async () => {
    const run = vi.fn(async () => {
      throw new S.AppError("source_http", "Jira said no.", "", false);
    });
    S.enqueue({ key: "3:scope", run });
    await advance(30_000);
    expect(run).toHaveBeenCalledTimes(1);
    expect(S.saveState.value).toBe("failed");
    expect(S.failedKeys.value.has("3:scope")).toBe(true);
  });

  it("puts_a_validation_error_on_the_field_by_key_and_never_retries", async () => {
    const err = new S.AppError("validation", "Name is too long.", "", false, "name");
    const run = vi.fn(async () => {
      throw err;
    });
    S.enqueue({ key: "3:name", run });
    await advance(30_000);
    expect(run).toHaveBeenCalledTimes(1);
    expect(S.fieldErrors.value.get("3:name")).toBe(err);
  });

  it("clears_the_field_error_once_a_later_save_for_that_key_succeeds", async () => {
    S.enqueue({
      key: "3:name",
      run: async () => {
        throw new S.AppError("validation", "Too long.", "", false, "name");
      },
    });
    await tick();
    expect(S.fieldErrors.value.has("3:name")).toBe(true);
    S.enqueue({ key: "3:name", run: async () => undefined });
    await tick();
    expect(S.fieldErrors.value.has("3:name")).toBe(false);
  });

  it("clears_a_failed_key_when_a_later_save_for_it_succeeds", async () => {
    S.enqueue({
      key: "3:scope",
      run: async () => {
        throw new S.AppError("source_http", "no", "", false);
      },
    });
    await tick();
    expect(S.failedKeys.value.has("3:scope")).toBe(true);
    S.enqueue({ key: "3:scope", run: async () => undefined });
    await tick();
    expect(S.failedKeys.value.has("3:scope")).toBe(false);
    expect(S.saveState.value).toBe("saved");
  });

  it("tracks_each_failed_key_separately", async () => {
    const fail = async () => {
      throw new S.AppError("source_http", "no", "", false);
    };
    S.enqueue({ key: "3:scope", run: fail });
    S.enqueue({ key: "3:rowsMode", run: fail });
    S.enqueue({ key: "3:pulseDays", run: async () => undefined });
    await tick();
    expect([...S.failedKeys.value].sort()).toEqual(["3:rowsMode", "3:scope"]);
  });

  it("retryFailed_re_runs_the_failed_keys_job_and_clears_it_on_success", async () => {
    const run = vi
      .fn<() => Promise<void>>()
      .mockRejectedValueOnce(new S.AppError("source_http", "no", "", false))
      .mockResolvedValueOnce(undefined);
    S.enqueue({ key: "3:scope", run });
    await tick();
    expect(S.saveState.value).toBe("failed");
    S.retryFailed();
    await tick();
    expect(run).toHaveBeenCalledTimes(2);
    expect(S.failedKeys.value.size).toBe(0);
    expect(S.saveState.value).toBe("saved");
  });

  it("retryFailed_re_runs_every_failed_key_once", async () => {
    const mk = () =>
      vi
        .fn<() => Promise<void>>()
        .mockRejectedValueOnce(new S.AppError("source_http", "no", "", false))
        .mockResolvedValue(undefined);
    const a = mk();
    const b = mk();
    S.enqueue({ key: "3:scope", run: a });
    S.enqueue({ key: "3:rowsMode", run: b });
    await tick();
    S.retryFailed();
    await tick();
    expect(a).toHaveBeenCalledTimes(2);
    expect(b).toHaveBeenCalledTimes(2);
  });

  it("retryFailed_does_nothing_when_nothing_failed", async () => {
    S.retryFailed();
    await tick();
    expect(S.saveState.value).toBe("idle");
    expect(S.failedKeys.value.size).toBe(0);
  });
});

describe("resetSaveQueue", () => {
  it("cancels_pending_debounces_and_clears_every_signal", async () => {
    const run = vi.fn(async () => undefined);
    S.enqueue({ key: "3:name", run, debounceMs: 400 });
    S.enqueue({
      key: "3:scope",
      run: async () => {
        throw new S.AppError("source_http", "no", "", false);
      },
    });
    await tick();
    expect(S.failedKeys.value.size).toBe(1);
    S.resetSaveQueue();
    await advance(10_000);
    expect(run).not.toHaveBeenCalled();
    expect(S.saveState.value).toBe("idle");
    expect(S.failedKeys.value.size).toBe(0);
    expect(S.fieldErrors.value.size).toBe(0);
  });
});
