// A minimal lazy component (no preact/compat): the chunk is fetched the first time the component renders, then kept.
// Used for rarely opened overlays (the command palette, the key legend, the Update dialog) so the entry bundle stays inside
// the 70 KB budget (architecture §6.1).
import type { ComponentType } from "preact";
import { useState } from "preact/hooks";

export function lazy<P extends object>(load: () => Promise<ComponentType<P>>): ComponentType<P> {
  let C: ComponentType<P> | null = null;
  let loading: Promise<void> | null = null;
  return function Lazy(props: P) {
    const [, update] = useState(0);
    if (C === null) {
      loading ??= load().then((c) => {
        C = c;
      });
      void loading.then(() => update(1));
      return null;
    }
    return <C {...props} />;
  };
}
