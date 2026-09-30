import { useEffect, useState } from "react";
import { api, displayName, type ModelDetail, type ModelSummary } from "./api";

export interface PickedPart {
  id: number;
  name: string;
  modelName: string;
  count: number;
}

// PartPicker finds parts of any model: search a model, tick the files of one of
// its variants (with a number of copies each) and add them.
export function PartPicker({ onAdd, skip = [], exclude = () => false, label = "Add" }: { onAdd: (parts: PickedPart[]) => void; skip?: number[]; exclude?: (name: string) => boolean; label?: string }) {
  const [q, setQ] = useState("");
  const [hits, setHits] = useState<ModelSummary[]>([]);
  const [model, setModel] = useState<ModelDetail | null>(null);
  const [variant, setVariant] = useState(0);
  const [ticked, setTicked] = useState<Record<number, number>>({}); // part id -> copies

  useEffect(() => {
    if (q.trim().length < 2) {
      setHits([]);
      return;
    }
    const t = setTimeout(() => {
      api.models({ q, creator: "", tag: "", collection: "", printed: "", hidden: "" }, 0, 8).then(setHits, () => setHits([]));
    }, 200);
    return () => clearTimeout(t);
  }, [q]);

  const open = (m: ModelSummary) =>
    api.model(m.id).then((d) => {
      setModel(d);
      setVariant(0);
      setTicked({});
      setHits([]);
      setQ("");
    });

  const v = model?.variants[variant];
  const parts = (v?.parts ?? []).filter((p) => !exclude(p.name) && !skip.includes(p.id));
  const toggle = (id: number) =>
    setTicked((t) => {
      const n = { ...t };
      if (n[id]) delete n[id];
      else n[id] = 1;
      return n;
    });
  const all = () => setTicked(Object.fromEntries(parts.map((p) => [p.id, ticked[p.id] ?? 1])));
  const add = () => {
    if (!model) return;
    onAdd(
      parts.filter((p) => ticked[p.id]).map((p) => ({ id: p.id, name: p.name, modelName: displayName(model), count: ticked[p.id] })),
    );
    setTicked({});
  };

  return (
    <div className="part-picker">
      <div className="tag-picker">
        <input value={q} onChange={(e) => setQ(e.target.value)} placeholder="search a model to take parts from…" />
        {hits.length > 0 && (
          <ul className="tag-options">
            {hits.map((m) => (
              <li
                key={m.id}
                onMouseDown={(e) => {
                  e.preventDefault();
                  open(m);
                }}
              >
                {displayName(m)} <span className="muted">{m.creator}</span>
              </li>
            ))}
          </ul>
        )}
      </div>
      {model && v && (
        <div className="picker-parts">
          <div className="picker-head">
            <strong>{displayName(model)}</strong>
            <select value={variant} onChange={(e) => (setVariant(Number(e.target.value)), setTicked({}))}>
              {model.variants.map((x, i) => (
                <option key={x.id} value={i}>
                  {x.label || "files"}
                  {x.option ? ` · ${x.option}` : ""}
                </option>
              ))}
            </select>
            <button className="link" onClick={all}>
              tick all
            </button>
            <button className="link" onClick={() => setTicked({})}>
              none
            </button>
          </div>
          <ul>
            {parts.map((p) => (
              <li key={p.id}>
                <label>
                  <input type="checkbox" checked={!!ticked[p.id]} onChange={() => toggle(p.id)} /> {p.name}
                </label>
                {ticked[p.id] && (
                  <input
                    type="number"
                    min={1}
                    max={10000}
                    value={ticked[p.id]}
                    onChange={(e) => setTicked((t) => ({ ...t, [p.id]: Math.max(1, Math.min(10000, Number(e.target.value) || 1)) }))}
                    aria-label={`Copies of ${p.name}`}
                  />
                )}
              </li>
            ))}
            {parts.length === 0 && <li className="muted">No parts to add here.</li>}
          </ul>
          <button onClick={add} disabled={Object.keys(ticked).length === 0}>
            {label} {Object.keys(ticked).length || ""} selected
          </button>
        </div>
      )}
    </div>
  );
}
