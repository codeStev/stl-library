import { useEffect, useState } from "react";
import { displayName, isSliced, type FileRef, type ModelDetail, type PartLink } from "./api";
import { PartPicker } from "./PartPicker";

interface Row {
  partId?: number; // unknown for a link whose file is gone from the library
  path: string;
  name: string;
  modelName?: string;
  count: number;
  missing?: boolean;
}

// SliceEditor says what a sliced file (a plate) contains: which part files, how many
// copies of each. Parts come from the model being viewed (if any) or from any other
// model, so a plate of many miniatures can be described too. It works for library
// plates and uploaded plates alike: the caller says how to load and save.
export function SliceEditor({
  title,
  load,
  save,
  model,
  onSaved,
  onCancel,
}: {
  title: string;
  load: () => Promise<{ contents: PartLink[]; usedIn?: PartLink[] }>;
  save: (items: { partId: number; count: number }[]) => Promise<unknown>;
  model?: ModelDetail;
  onSaved: () => void;
  onCancel: () => void;
}) {
  const [rows, setRows] = useState<Row[] | null>(null);
  const [usedIn, setUsedIn] = useState<PartLink[]>([]);
  const [error, setError] = useState("");

  useEffect(() => {
    load().then(
      (r) => {
        setRows(r.contents.map((c) => ({ partId: c.partId, path: c.path, name: c.name, modelName: c.modelName, count: c.count, missing: c.missing })));
        setUsedIn(r.usedIn ?? []);
      },
      (e) => setError(String(e)),
    );
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  if (!rows) return <div className="slice-editor">{error || "Loading…"}</div>;

  const has = (id: number) => rows.some((r) => r.partId === id);
  const setCount = (i: number, count: number) => setRows(rows.map((r, k) => (k === i ? { ...r, count: Math.max(1, Math.min(10000, count || 1)) } : r)));
  const doSave = () =>
    save(rows.filter((r) => r.partId).map((r) => ({ partId: r.partId as number, count: r.count }))).then(onSaved, (e) => setError(String(e)));
  const own = model ? model.variants.flatMap((v) => v.parts.map((p) => ({ p, v }))).filter(({ p }) => !isSliced(p.name) && !has(p.id)) : [];
  const addFile = (p: FileRef, from: string, count = 1) => setRows((rs) => (rs ?? []).some((r) => r.partId === p.id) ? rs : [...(rs ?? []), { partId: p.id, path: p.name, name: p.name, modelName: from, count }]);

  return (
    <div className="slice-editor">
      <strong>{title} contains</strong>
      {rows.length === 0 && <p className="muted">Nothing linked yet.</p>}
      <ul>
        {rows.map((r, i) => (
          <li key={r.partId ?? r.path} className={r.missing ? "missing" : ""}>
            <input type="number" min={1} max={10000} value={r.count} onChange={(e) => setCount(i, Number(e.target.value))} aria-label={`Copies of ${r.name}`} />
            <span>×</span>
            <span className="name">{r.name}</span>
            {r.modelName && <span className="muted">{r.modelName}</span>}
            {r.missing && <span className="muted">(file not in the library any more - dropped when you save)</span>}
            <button className="link" onClick={() => setRows(rows.filter((_, k) => k !== i))} aria-label={`Remove ${r.name}`}>
              ✕
            </button>
          </li>
        ))}
      </ul>
      {model && own.length > 0 && (
        <div className="slice-add">
          <select
            value=""
            onChange={(e) => {
              const hit = own.find(({ p }) => String(p.id) === e.target.value);
              if (hit) addFile(hit.p, displayName(model));
            }}
          >
            <option value="">add a part of this model…</option>
            {own.map(({ p, v }) => (
              <option key={p.id} value={p.id}>
                {p.name} — {v.label || "files"}
              </option>
            ))}
          </select>
        </div>
      )}
      <PartPicker
        exclude={isSliced}
        skip={rows.map((r) => r.partId ?? 0)}
        onAdd={(ps) => ps.forEach((p) => addFile({ id: p.id, name: p.name, size: 0 }, p.modelName, p.count))}
      />
      {usedIn.length > 0 && <p className="muted">This file is itself in: {usedIn.map((u) => `${u.name} ×${u.count}`).join(", ")}</p>}
      {error && <p className="error">{error}</p>}
      <div className="actions">
        <button onClick={doSave}>Save</button>
        <button className="link" onClick={onCancel}>
          Cancel
        </button>
      </div>
    </div>
  );
}
