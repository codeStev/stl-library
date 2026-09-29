import { useEffect, useState } from "react";
import { api, displayName, isSliced, type FileRef, type ModelDetail, type ModelSummary, type PartLink } from "./api";

interface Row {
  partId?: number; // unknown for a link whose file is gone from the library
  path: string;
  name: string;
  modelName?: string;
  count: number;
  missing?: boolean;
}

// SliceEditor says what a sliced file (a plate) contains: which part files,
// how many copies of each. Parts are picked from this model or from another
// model, so a plate of many miniatures can be described too.
export function SliceEditor({ part, model, onSaved, onCancel }: { part: FileRef; model: ModelDetail; onSaved: () => void; onCancel: () => void }) {
  const [rows, setRows] = useState<Row[] | null>(null);
  const [usedIn, setUsedIn] = useState<PartLink[]>([]);
  const [error, setError] = useState("");
  const [other, setOther] = useState<ModelDetail | null>(null);

  useEffect(() => {
    api.sliceContents(part.id).then(
      (r) => {
        setRows(r.contents.map((c) => ({ partId: c.partId, path: c.path, name: c.name, modelName: c.modelName, count: c.count, missing: c.missing })));
        setUsedIn(r.usedIn);
      },
      (e) => setError(String(e)),
    );
  }, [part.id]);

  if (!rows) return <div className="slice-editor">{error || "Loading…"}</div>;

  const has = (id: number) => rows.some((r) => r.partId === id);
  const add = (p: FileRef, from: ModelDetail) => {
    if (has(p.id)) return;
    setRows([...rows, { partId: p.id, path: p.name, name: p.name, modelName: displayName(from), count: 1 }]);
  };
  const setCount = (i: number, count: number) => setRows(rows.map((r, k) => (k === i ? { ...r, count: Math.max(1, Math.min(10000, count || 1)) } : r)));
  const save = () =>
    api
      .setSliceContents(
        part.id,
        rows.filter((r) => r.partId).map((r) => ({ partId: r.partId as number, count: r.count })),
      )
      .then(onSaved, (e) => setError(String(e)));

  // This model's files that can go into a plate: models and projects, not the plate itself.
  const own = model.variants.flatMap((v) => v.parts.map((p) => ({ p, v }))).filter(({ p }) => p.id !== part.id);

  return (
    <div className="slice-editor">
      <strong>{part.name} contains</strong>
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
      <div className="slice-add">
        <select
          value=""
          onChange={(e) => {
            const hit = own.find(({ p }) => String(p.id) === e.target.value);
            if (hit) add(hit.p, model);
          }}
        >
          <option value="">add a part of this model…</option>
          {own
            .filter(({ p }) => !has(p.id))
            .map(({ p, v }) => (
              <option key={p.id} value={p.id}>
                {p.name} — {v.label || "files"}
              </option>
            ))}
        </select>
        <OtherModelPicker current={model.id} onModel={setOther} />
      </div>
      {other && (
        <div className="slice-add">
          <span className="muted">{displayName(other)}:</span>
          <select
            value=""
            onChange={(e) => {
              const hit = other.variants.flatMap((v) => v.parts).find((p) => String(p.id) === e.target.value);
              if (hit) add(hit, other);
            }}
          >
            <option value="">add a part…</option>
            {other.variants.flatMap((v) =>
              v.parts
                .filter((p) => !has(p.id) && !isSliced(p.name))
                .map((p) => (
                  <option key={p.id} value={p.id}>
                    {p.name} — {v.label || "files"}
                  </option>
                )),
            )}
          </select>
          <button className="link" onClick={() => setOther(null)}>
            close
          </button>
        </div>
      )}
      {usedIn.length > 0 && (
        <p className="muted">
          This file is itself in: {usedIn.map((u) => `${u.name} ×${u.count}`).join(", ")}
        </p>
      )}
      {error && <p className="error">{error}</p>}
      <div className="actions">
        <button onClick={save}>Save</button>
        <button className="link" onClick={onCancel}>
          Cancel
        </button>
      </div>
    </div>
  );
}

// OtherModelPicker searches the library for a model whose parts can go into
// the plate, too (a plate of miniatures from several models).
function OtherModelPicker({ current, onModel }: { current: number; onModel: (m: ModelDetail) => void }) {
  const [q, setQ] = useState("");
  const [hits, setHits] = useState<ModelSummary[]>([]);
  useEffect(() => {
    if (q.trim().length < 2) {
      setHits([]);
      return;
    }
    const t = setTimeout(() => {
      api.models({ q, creator: "", tag: "", printed: "", hidden: "" }, 0, 8).then((ms) => setHits(ms.filter((m) => m.id !== current)), () => setHits([]));
    }, 200);
    return () => clearTimeout(t);
  }, [q, current]);
  return (
    <span className="tag-picker">
      <input value={q} onChange={(e) => setQ(e.target.value)} placeholder="…or search another model" />
      {hits.length > 0 && (
        <ul className="tag-options">
          {hits.map((m) => (
            <li
              key={m.id}
              onMouseDown={(e) => {
                e.preventDefault();
                api.model(m.id).then(onModel);
                setQ("");
                setHits([]);
              }}
            >
              {displayName(m)} <span className="muted">{m.creator}</span>
            </li>
          ))}
        </ul>
      )}
    </span>
  );
}
