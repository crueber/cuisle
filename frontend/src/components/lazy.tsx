// A minimal lazy component (no preact/compat): the chunk is fetched the first time the component renders, then kept.
// Used for rarely opened overlays (the command palette, the key legend, the Update dialog) so the entry bundle stays inside
// the 70 KB budget (architecture §6.1).
import type { ComponentType } from "preact";
import { useEffect, useState } from "preact/hooks";

export function lazy<P extends object>(load: () => Promise<ComponentType<P>>, onError?: () => void): ComponentType<P> {
  let C: ComponentType<P> | null = null;
  let loading: Promise<void> | null = null;
  return function Lazy(props: P) {
    const [, update] = useState(0);
    useEffect(() => {
      if (C !== null) return;
      let alive = true;
      loading ??= load()
        .then((c) => {
          C = c;
        })
        .catch(() => {
          loading = null;
        });
      void loading.then(() => {
        if (!alive) return;
        if (C !== null) update((n) => n + 1);
        else onError?.();
      });
      return () => {
        alive = false;
      };
    }, []);
    if (C === null) return null;
    return <C {...props} />;
  };
}
