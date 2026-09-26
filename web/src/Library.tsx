import { useEffect, useRef, useState } from "react";
import { api, displayName, type Creator, type Filters, type ModelSummary, type Tag } from "./api";

const PAGE = 60;
const empty: Filters = { q: "", creator: "", tag: "", printed: "" };

function loadFilters(): Filters {
  try {
    return { ...empty, ...JSON.parse(sessionStorage.getItem("filters") ?? "{}") };
  } catch {
    return empty;
  }
}

// The library grid: search, filters (creator, tag, printed), previews,
// "load more" paging. Filters live in sessionStorage so going back to the
// grid keeps them.
export function Library() {
  const [f, setF] = useState<Filters>(loadFilters);
  const [creators, setCreators] = useState<Creator[]>([]);
  const [tags, setTags] = useState<Tag[]>([]);
  const [models, setModels] = useState<ModelSummary[]>([]);
  const [more, setMore] = useState(false);
  const [error, setError] = useState("");
  const seq = useRef(0);
  const set = (patch: Partial<Filters>) => setF((prev) => ({ ...prev, ...patch }));

  useEffect(() => {
    api.creators().then(setCreators, (e) => setError(String(e)));
    api.tags().then(setTags, () => {});
  }, []);

  useEffect(() => {
    sessionStorage.setItem("filters", JSON.stringify(f));
    const mine = ++seq.current;
    const t = setTimeout(() => {
      api.models(f, 0, PAGE).then(
        (ms) => {
          if (mine !== seq.current) return; // a newer search is on its way
          setModels(ms);
          setMore(ms.length === PAGE);
        },
        (e) => setError(String(e)),
      );
    }, 200);
    return () => clearTimeout(t);
  }, [f]);

  const loadMore = () =>
    api.models(f, models.length, PAGE).then((ms) => {
      setModels((prev) => [...prev, ...ms]);
      setMore(ms.length === PAGE);
    });

  return (
    <>
      <div className="toolbar">
        <input
          type="search"
          placeholder="Search models, creators, releases, tags…"
          value={f.q}
          onChange={(e) => set({ q: e.target.value })}
          autoFocus
        />
        <select value={f.creator} onChange={(e) => set({ creator: e.target.value })}>
          <option value="">All creators</option>
          {creators.map((c) => (
            <option key={c.name} value={c.name}>
              {c.name} ({c.models})
            </option>
          ))}
        </select>
        {tags.length > 0 && (
          <select value={f.tag} onChange={(e) => set({ tag: e.target.value })}>
            <option value="">All tags</option>
            {tags.map((t) => (
              <option key={t.tag} value={t.tag}>
                {t.tag} ({t.models})
              </option>
            ))}
          </select>
        )}
        <select value={f.printed} onChange={(e) => set({ printed: e.target.value as Filters["printed"] })}>
          <option value="">Printed or not</option>
          <option value="yes">Printed</option>
          <option value="no">Never printed</option>
        </select>
      </div>
      {error && <p className="error">{error}</p>}
      <div className="grid">
        {models.map((m) => (
          <a key={m.id} className="card" href={`#/model/${m.id}`}>
            <div className="cover">
              {m.preview ? <img src={api.previewURL(m.id)} alt="" loading="lazy" /> : <span>no preview</span>}
              {m.prints > 0 && <span className="badge">printed{m.prints > 1 ? ` ×${m.prints}` : ""}</span>}
            </div>
            <div className="title">{displayName(m)}</div>
            <div className="sub">
              {m.creator}
              {m.release && ` · ${m.release}`}
            </div>
            <div className="sub">
              {m.variants} variant{m.variants === 1 ? "" : "s"}
            </div>
            {m.tags.length > 0 && (
              <div className="tags">
                {m.tags.map((t) => (
                  <span key={t} className="tag">
                    {t}
                  </span>
                ))}
              </div>
            )}
          </a>
        ))}
      </div>
      {models.length === 0 && !error && <p className="empty">No models found.</p>}
      {more && (
        <button className="more" onClick={loadMore}>
          Load more
        </button>
      )}
    </>
  );
}
