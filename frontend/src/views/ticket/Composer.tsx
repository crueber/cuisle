// Composer: the comment box at the head of the Comments section (ux-spec §14.5; props.ts ComposerProps).
// Prop driven: it never calls the api. The state layer owns the draft (debounce, flush on blur, clearing after send).
// "@" opens the shared people Combobox; choosing a person inserts "@Name " and records a rune-offset Mention.
import { useLayoutEffect, useRef, useState } from "preact/hooks";
import type { JSX } from "preact";
import type { CommentInput, Mention, UserOption } from "../../api/types";
import { Combobox } from "../../components/Combobox";
import type { ComposerProps } from "./props";
import "./Composer.css";

const runes = (s: string): string[] => Array.from(s);

/** Keeps mentions across an edit: those before the changed span stay put, those after it shift, the rest drop. */
function carryMentions(prev: CommentInput, next: string): Mention[] {
  const a = runes(prev.text);
  const b = runes(next);
  let head = 0;
  while (head < a.length && head < b.length && a[head] === b[head]) head++;
  let tail = 0;
  while (tail < a.length - head && tail < b.length - head && a[a.length - 1 - tail] === b[b.length - 1 - tail]) tail++;
  const delta = b.length - a.length;
  const out: Mention[] = [];
  for (const m of prev.mentions ?? []) {
    if (m.end <= head) out.push(m);
    else if (m.start >= a.length - tail) out.push({ ...m, start: m.start + delta, end: m.end + delta });
  }
  return out;
}

/** Replaces the "@" at rune index `at` with "@Name " and records the mention. */
function insertMention(input: CommentInput, at: number, user: UserOption): CommentInput {
  const r = runes(input.text);
  const token = runes(`@${user.displayName} `);
  const delta = token.length - 1;
  const text = [...r.slice(0, at), ...token, ...r.slice(at + 1)].join("");
  const mention: Mention = { start: at, end: at + token.length - 1, accountId: user.accountId, name: user.displayName };
  const moved = (input.mentions ?? []).map((m) => (m.start > at ? { ...m, start: m.start + delta, end: m.end + delta } : m));
  return { text, mentions: [...moved, mention].sort((x, y) => x.start - y.start) };
}

export function Composer({
  draft,
  selfName,
  selfInitials,
  disabled,
  disabledReason,
  focusToken,
  onChange,
  onSend,
  onBlur,
  searchMentions,
}: ComposerProps) {
  const boxRef = useRef<HTMLTextAreaElement>(null);
  const seenToken = useRef(focusToken);
  const [at, setAt] = useState<number | null>(null); // rune index of the "@" that opened the people picker
  // The picker is live only while its "@" is still in the draft (the draft can change from outside: send, Edit, next ticket).
  const picking = at !== null && runes(draft.text)[at] === "@";
  const canSend = !disabled && draft.text.trim() !== "";

  useLayoutEffect(() => {
    if (focusToken === seenToken.current) return;
    seenToken.current = focusToken;
    boxRef.current?.focus();
  }, [focusToken]);

  const send = () => {
    if (canSend) onSend({ text: draft.text, mentions: draft.mentions ?? [] });
  };

  const onKeyDown = (e: JSX.TargetedKeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === "Enter" && (e.ctrlKey || e.metaKey)) {
      e.preventDefault();
      send();
    } else if (e.key === "Escape" && picking) {
      e.preventDefault();
      setAt(null);
    }
  };

  const onInput = (e: JSX.TargetedEvent<HTMLTextAreaElement, Event>) => {
    const el = e.currentTarget;
    const text = el.value;
    const caret = runes(text.slice(0, el.selectionStart ?? text.length)).length;
    const chars = runes(text);
    if (text.length > draft.text.length && caret > 0 && chars[caret - 1] === "@") setAt(caret - 1);
    else if (at !== null && chars[at] !== "@") setAt(null);
    onChange({ text, mentions: carryMentions(draft, text) });
  };

  const pick = (user: UserOption) => {
    if (!picking || at === null) return;
    onChange(insertMention(draft, at, user));
    setAt(null);
    boxRef.current?.focus();
  };

  return (
    <div class="cmp" data-disabled={disabled ? "true" : undefined}>
      <span class="cmp-badge" aria-hidden="true">
        {selfInitials}
      </span>
      <div class="cmp-main">
        <textarea
          ref={boxRef}
          class="cmp-box"
          aria-label="Comment"
          rows={2}
          value={draft.text}
          disabled={disabled}
          placeholder="Write a comment…"
          onInput={onInput}
          onKeyDown={onKeyDown}
          onBlur={() => onBlur()}
        />
        {picking ? (
          <div class="cmp-picker">
            <Combobox<UserOption>
              id="cmp-mention"
              label="Mention people"
              value={null}
              getKey={(u) => u.accountId}
              getLabel={(u) => u.displayName}
              load={searchMentions}
              onSelect={pick}
              placeholder="Find a person…"
            />
          </div>
        ) : null}
        <div class="cmp-foot">
          <span class="cmp-hint">
            Ctrl+Enter to send · posts to Jira as {selfName}
          </span>
          {disabled && disabledReason ? <span class="cmp-reason">{disabledReason}</span> : null}
          <button type="button" class="cmp-send" disabled={!canSend} onClick={send}>
            Send
          </button>
        </div>
      </div>
    </div>
  );
}
