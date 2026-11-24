// FROZEN CONTRACT (implemented in Wave 0). Typed Go → JS events (architecture §3.3).
import { Events } from "@wailsio/runtime";
import type * as T from "./types";

export interface EventMap {
  "sync:started": T.SyncStarted;
  "sync:progress": T.SyncProgress;
  "sync:finished": T.SyncFinished;
  "data:changed": T.DataChanged;
  "connection:auth": T.ConnectionAuth;
  "sink:delivered": T.SinkDelivered;
  "sink:failed": T.SinkFailed;
  "day:rolled": T.DayRolled;
  "ticket:updated": T.TicketUpdated;
  "ticket:write_failed": T.TicketWriteFailed;
  "media:ready": T.MediaReady;
  "sync:throttled": T.SyncThrottled;
}
export type EventName = keyof EventMap;

/** An event bus: the Wails one in the app, an in-memory one in tests and mock mode. */
export interface EventBus {
  on<E extends EventName>(name: E, cb: (payload: EventMap[E]) => void): () => void;
}

export const wailsBus: EventBus = {
  on(name, cb) {
    return Events.On(name, (ev: { data: unknown }) => cb(ev.data as never));
  },
};

/** In-memory bus with emit (tests, mock mode). */
export function createMemoryBus(): EventBus & { emit<E extends EventName>(name: E, payload: EventMap[E]): void } {
  const subs = new Map<EventName, Set<(p: unknown) => void>>();
  return {
    on(name, cb) {
      let set = subs.get(name);
      if (!set) subs.set(name, (set = new Set()));
      const f = cb as (p: unknown) => void;
      set.add(f);
      return () => set!.delete(f);
    },
    emit(name, payload) {
      subs.get(name)?.forEach((f) => f(payload));
    },
  };
}

let bus: EventBus = wailsBus;

/** Subscribe; returns the unsubscribe function. */
export function on<E extends EventName>(name: E, cb: (payload: EventMap[E]) => void): () => void {
  return bus.on(name, cb);
}

export function setEventBus(b: EventBus): void {
  bus = b;
}
