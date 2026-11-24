// Block components with state: expand (collapsible, E-key aware). Children are rendered by AdfNode.
import type { ComponentChildren } from "preact";
import { useLayoutEffect, useRef, useState } from "preact/hooks";
import type { AdfContext } from "./props";

export function Expand({
  title,
  collapsed,
  ctx,
  children,
}: {
  title: string;
  collapsed: boolean;
  ctx: AdfContext;
  children?: ComponentChildren;
}) {
  const [open, setOpen] = useState(!collapsed);
  const seen = useRef<number | undefined>(undefined);
  const all = ctx.expandAll;

  // A token this expand has not seen forces its state (the E key). A re-render with the same token keeps manual toggles.
  useLayoutEffect(() => {
    if (!all || all.token === seen.current) return;
    seen.current = all.token;
    setOpen(all.open);
  }, [all]);

  return (
    <div class="adf-expand" data-adf="expand" data-open={open ? "true" : "false"}>
      <button
        type="button"
        class="adf-expand-toggle"
        aria-expanded={open ? "true" : "false"}
        onClick={() => setOpen(!open)}
      >
        <span class="adf-chevron" aria-hidden="true">
          {open ? "▾" : "▸"}
        </span>
        <span class="adf-expand-title">{title || "Expand"}</span>
      </button>
      {open ? <div class="adf-expand-body">{children}</div> : null}
    </div>
  );
}
