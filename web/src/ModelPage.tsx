import { useEffect, useMemo, useState } from "react";
import { api, formatBytes, type DimKey, type ModelDetail, type Variant } from "./api";

type Key = DimKey | "option";
const KEYS: { key: Key; label: string }[] = [
  { key: "scale", label: "Scale" },
  { key: "supports", label: "Supports" },
  { key: "density", label: "Density" },
  { key: "format", label: "Format" },
  { key: "split", label: "Split" },
  { key: "extra", label: "Extra" },
  { key: "fill", label: "Fill" },
  { key: "tech", label: "Tech" },
  { key: "option", label: "Option" },
];

const value = (v: Variant, k: Key): string => (k === "option" ? v.option : v.dims[k]) ?? "—";

// pick returns the variant with dimension k = val that agrees with the
// current variant on as many other dimensions as possible.
function pick(variants: Variant[], current: Variant, k: Key, val: string): Variant {
  let best = current;
  let score = -1;
  for (const v of variants) {
    if (value(v, k) !== val) continue;
    const s = KEYS.filter(({ key }) => value(v, key) === value(current, key)).length;
    if (s > score) [best, score] = [v, s];
  }
  return best;
}

export function ModelPage({ id }: { id: number }) {
  const [m, setM] = useState<ModelDetail | null>(null);
  const [sel, setSel] = useState<Variant | null>(null);
  const [image, setImage] = useState(0);
  const [error, setError] = useState("");

  useEffect(() => {
    setM(null);
    api.model(id).then(
      (m) => {
        setM(m);
        setSel(m.variants[0] ?? null);
        setImage(0);
      },
      (e) => setError(String(e)),
    );
  }, [id]);

  // Only dimensions in which the variants actually differ get chips.
  const dims = useMemo(() => {
    if (!m) return [];
    return KEYS.map(({ key, label }) => ({
      key,
      label,
      values: [...new Set(m.variants.map((v) => value(v, key)))],
    })).filter((d) => d.values.length > 1);
  }, [m]);

  if (error) return <p className="error">{error}</p>;
  if (!m) return <p>Loading…</p>;
  const pictures = m.images.filter((f) => /\.(jpe?g|png|webp|gif)$/i.test(f.name));
  const docs = m.images.filter((f) => !/\.(jpe?g|png|webp|gif)$/i.test(f.name));

  return (
    <div className="model">
      <div className="crumbs">
        <a href="#/" onClick={() => sessionStorage.setItem("creator", m.creator)}>
          {m.creator}
        </a>
        {m.release && <> › {m.release}</>}
        {m.category && <> › {m.category}</>}
      </div>
      <h1>{m.name}</h1>
      <div className="model-body">
        <section className="gallery">
          {pictures.length > 0 ? (
            <>
              <img className="main" src={api.imageURL(pictures[image].id)} alt={pictures[image].name} />
              {pictures.length > 1 && (
                <div className="thumbs">
                  {pictures.map((p, i) => (
                    <img
                      key={p.id}
                      src={api.thumbURL(p.id)}
                      alt={p.name}
                      loading="lazy"
                      className={i === image ? "active" : ""}
                      onClick={() => setImage(i)}
                    />
                  ))}
                </div>
              )}
            </>
          ) : m.preview ? (
            <img className="main render" src={api.previewURL(m.id)} alt={`${m.name} (rendered)`} />
          ) : (
            <div className="noimage">no image</div>
          )}
          {docs.length > 0 && (
            <ul className="docs">
              {docs.map((d) => (
                <li key={d.id}>
                  <a href={api.imageURL(d.id)} target="_blank" rel="noreferrer">
                    {d.name}
                  </a>
                </li>
              ))}
            </ul>
          )}
        </section>
        <section className="variants">
          {sel ? (
            <>
              {dims.map((d) => (
                <div key={d.key} className="dim">
                  <span className="dim-label">{d.label}</span>
                  {d.values.map((val) => {
                    const target = pick(m.variants, sel, d.key, val);
                    const active = value(sel, d.key) === val;
                    return (
                      <button
                        key={val}
                        className={`chip${active ? " active" : ""}`}
                        onClick={() => setSel(target)}
                      >
                        {val}
                      </button>
                    );
                  })}
                </div>
              ))}
              <h2>
                {sel.label || "Files"}
                {sel.option && <span className="option"> · {sel.option}</span>}
              </h2>
              <a className="download" href={api.zipURL(sel.id)}>
                Download zip ({sel.parts.length} files, {formatBytes(sel.parts.reduce((n, p) => n + p.size, 0))})
              </a>
              <ul className="parts">
                {sel.parts.map((p) => (
                  <li key={p.id}>
                    <a href={api.partURL(p.id)}>{p.name}</a>
                    <span>{formatBytes(p.size)}</span>
                  </li>
                ))}
              </ul>
            </>
          ) : (
            <p>This model has no print files.</p>
          )}
        </section>
      </div>
      <p className="path">{m.dir}</p>
    </div>
  );
}
