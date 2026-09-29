import { useEffect, useRef, useState } from "react";
import type { Tag } from "./api";

// TagPicker offers the tags that exist already, so a tag is picked, not
// retyped. A name that matches none can only be made on purpose, with the
// "Create" entry - a typo doesn't quietly become a new tag.
export function TagPicker({
  existing,
  skip = [],
  placeholder = "add tag…",
  onPick,
}: {
  existing: Tag[];
  skip?: string[]; // tags not offered (already on the model)
  placeholder?: string;
  onPick: (tag: string) => void;
}) {
  const [text, setText] = useState("");
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(0);
  const box = useRef<HTMLDivElement>(null);

  const q = text.trim().toLowerCase();
  const skipped = new Set(skip.map((s) => s.toLowerCase()));
  const matches = existing
    .filter((t) => !skipped.has(t.tag.toLowerCase()) && (q === "" || t.tag.toLowerCase().includes(q)))
    .sort((a, b) => Number(b.tag.toLowerCase().startsWith(q)) - Number(a.tag.toLowerCase().startsWith(q)) || b.models - a.models)
    .slice(0, 12);
  const exact = existing.some((t) => t.tag.toLowerCase() === q);
  const canCreate = q !== "" && !exact && !skipped.has(q);
  const rows: { tag: string; create: boolean; models?: number }[] = [
    ...matches.map((t) => ({ tag: t.tag, create: false, models: t.models })),
    ...(canCreate ? [{ tag: text.trim(), create: true }] : []),
  ];

  useEffect(() => setActive(0), [text]);
  useEffect(() => {
    const close = (e: MouseEvent) => box.current && !box.current.contains(e.target as Node) && setOpen(false);
    document.addEventListener("mousedown", close);
    return () => document.removeEventListener("mousedown", close);
  }, []);

  const pick = (tag: string) => {
    onPick(tag);
    setText("");
    setOpen(false);
  };

  return (
    <div className="tag-picker" ref={box}>
      <input
        value={text}
        placeholder={placeholder}
        maxLength={200}
        role="combobox"
        aria-expanded={open}
        onFocus={() => setOpen(true)}
        onChange={(e) => {
          setText(e.target.value);
          setOpen(true);
        }}
        onKeyDown={(e) => {
          if (e.key === "ArrowDown") {
            e.preventDefault();
            setActive((a) => Math.min(a + 1, rows.length - 1));
          } else if (e.key === "ArrowUp") {
            e.preventDefault();
            setActive((a) => Math.max(a - 1, 0));
          } else if (e.key === "Enter") {
            e.preventDefault();
            const r = rows[active];
            if (r) pick(r.tag);
          } else if (e.key === "Escape") {
            setOpen(false);
          }
        }}
      />
      {open && rows.length > 0 && (
        <ul className="tag-options" role="listbox">
          {rows.map((r, i) => (
            <li
              key={(r.create ? "+" : "") + r.tag}
              role="option"
              aria-selected={i === active}
              className={(i === active ? "active " : "") + (r.create ? "create" : "")}
              onMouseEnter={() => setActive(i)}
              onMouseDown={(e) => {
                e.preventDefault();
                pick(r.tag);
              }}
            >
              {r.create ? (
                <>
                  Create new tag “<b>{r.tag}</b>”
                </>
              ) : (
                <>
                  {r.tag} <span className="muted">{r.models}</span>
                </>
              )}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
