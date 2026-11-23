// TextField (ux-spec §9.3, 38px).
//
// SPEC-NOTE: The control is found by its label (getByLabelText), which also works for type="password" (no textbox role).
// SPEC-NOTE: onInput fires on every keystroke with the full new value; onCommit fires on blur and on Enter (single-line)
//   with the current value. In multiline mode Enter inserts a newline and does NOT commit; blur does. Whether Enter
//   followed by blur commits once or twice is not specified.
// SPEC-NOTE: aria-invalid="true" and aria-describedby (resolving to the error text) are set only while `error` is
//   non-empty. `hint` is linked through aria-describedby too when there is no error; with both, the error text is
//   among the described-by content.

import { render, screen } from "@testing-library/preact";
import userEvent from "@testing-library/user-event";
import axe from "axe-core";
import { useState } from "preact/hooks";
import { TextField } from "./TextField";
import type { TextFieldProps } from "./props";

function props(over: Partial<TextFieldProps> = {}): TextFieldProps {
  return { id: "tf", label: "Board URL", value: "", onInput: () => {}, ...over };
}
const field = () => screen.getByLabelText("Board URL") as HTMLInputElement | HTMLTextAreaElement;
const describedText = (el: HTMLElement) =>
  (el.getAttribute("aria-describedby") ?? "")
    .split(/\s+/)
    .filter(Boolean)
    .map((id) => document.getElementById(id)?.textContent ?? "")
    .join(" ");

function Harness(p: { init?: string; over?: Partial<TextFieldProps>; onInput?: (v: string) => void; onCommit?: (v: string) => void }) {
  const [v, setV] = useState(p.init ?? "");
  return (
    <TextField
      {...props(p.over)}
      value={v}
      onInput={(n) => {
        setV(n);
        p.onInput?.(n);
      }}
      onCommit={p.onCommit}
    />
  );
}

describe("TextField", () => {
  it("renders a single-line input labelled by label with the given value, placeholder and maxLength", () => {
    render(<TextField {...props({ value: "abc", placeholder: "https://…", maxLength: 40 })} />);
    const el = field();
    expect(el.tagName).toBe("INPUT");
    expect(el.value).toBe("abc");
    expect(el.getAttribute("placeholder")).toBe("https://…");
    expect(el.maxLength).toBe(40);
  });

  it("renders a textarea when multiline", () => {
    render(<TextField {...props({ value: "line1\nline2", multiline: true })} />);
    expect(field().tagName).toBe("TEXTAREA");
    expect(field().value).toBe("line1\nline2");
  });

  it.each(["text", "url", "email", "password"] as const)("uses input type %s", (type) => {
    render(<TextField {...props({ type })} />);
    expect((field() as HTMLInputElement).type).toBe(type);
  });

  it("calls onInput with the full new value on every keystroke", async () => {
    const user = userEvent.setup();
    const onInput = vi.fn();
    render(<Harness onInput={onInput} />);
    await user.type(field(), "abc");
    expect(onInput.mock.calls.map((c) => c[0])).toEqual(["a", "ab", "abc"]);
  });

  it("does not call onCommit while typing", async () => {
    const user = userEvent.setup();
    const onCommit = vi.fn();
    render(<Harness onCommit={onCommit} />);
    await user.type(field(), "abc");
    expect(field().value).toBe("abc");
    expect(onCommit).not.toHaveBeenCalled();
  });

  it("calls onCommit with the current value on blur", async () => {
    const user = userEvent.setup();
    const onCommit = vi.fn();
    render(<Harness onCommit={onCommit} />);
    await user.type(field(), "xyz");
    await user.tab();
    expect(onCommit).toHaveBeenCalledTimes(1);
    expect(onCommit).toHaveBeenCalledWith("xyz");
  });

  it("calls onCommit with the current value on Enter", async () => {
    const user = userEvent.setup();
    const onCommit = vi.fn();
    render(<Harness onCommit={onCommit} />);
    await user.type(field(), "xyz{Enter}");
    expect(onCommit).toHaveBeenCalled();
    expect(onCommit).toHaveBeenLastCalledWith("xyz");
  });

  it("does not commit on Enter in multiline mode, but does on blur", async () => {
    const user = userEvent.setup();
    const onCommit = vi.fn();
    render(<Harness over={{ multiline: true }} onCommit={onCommit} />);
    await user.type(field(), "a{Enter}b");
    expect(field().value).toBe("a\nb");
    expect(onCommit).not.toHaveBeenCalled();
    await user.tab();
    expect(onCommit).toHaveBeenCalledTimes(1);
    expect(onCommit).toHaveBeenCalledWith("a\nb");
  });

  it("works without onCommit", async () => {
    const user = userEvent.setup();
    render(<Harness />);
    await user.type(field(), "ok{Enter}");
    await user.tab();
    expect(field().value).toBe("ok");
  });

  it("marks the field invalid and describes it with the error message", () => {
    render(<TextField {...props({ error: "Enter a valid URL" })} />);
    const el = field();
    expect(el.getAttribute("aria-invalid")).toBe("true");
    expect(screen.getByText("Enter a valid URL")).toBeTruthy();
    expect(describedText(el)).toContain("Enter a valid URL");
  });

  it("is not invalid without an error", () => {
    render(<TextField {...props()} />);
    expect(["false", null]).toContain(field().getAttribute("aria-invalid"));
  });

  it("links a hint through aria-describedby when there is no error", () => {
    render(<TextField {...props({ hint: "Paste the board address" })} />);
    expect(screen.getByText("Paste the board address")).toBeTruthy();
    expect(describedText(field())).toContain("Paste the board address");
  });

  it("includes the error among the described-by content when a hint is also present", () => {
    render(<TextField {...props({ hint: "Paste the board address", error: "Enter a valid URL" })} />);
    expect(field().getAttribute("aria-invalid")).toBe("true");
    expect(describedText(field())).toContain("Enter a valid URL");
  });

  it("focuses the field on mount when autoFocus is set", () => {
    render(<TextField {...props({ autoFocus: true })} />);
    expect(document.activeElement).toBe(field());
  });

  it("has no axe violations (plain, error, hint, multiline, password)", async () => {
    const { container, rerender } = render(<TextField {...props({ value: "x" })} />);
    expect(field()).toBeTruthy();
    expect((await axe.run(container)).violations.map((v) => v.id)).toEqual([]);
    rerender(<TextField {...props({ error: "Enter a valid URL", hint: "Paste the board address" })} />);
    expect(screen.getByText("Enter a valid URL")).toBeTruthy();
    expect((await axe.run(container)).violations.map((v) => v.id)).toEqual([]);
    rerender(<TextField {...props({ multiline: true })} />);
    expect(field().tagName).toBe("TEXTAREA");
    expect((await axe.run(container)).violations.map((v) => v.id)).toEqual([]);
    rerender(<TextField {...props({ type: "password" })} />);
    expect((field() as HTMLInputElement).type).toBe("password");
    expect((await axe.run(container)).violations.map((v) => v.id)).toEqual([]);
  });
});
