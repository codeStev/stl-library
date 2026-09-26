import { useEffect, useRef, useState } from "react";
import { api, type Creator, type ModelSummary } from "./api";

const PAGE = 60;

// The library grid: search, creator filter, covers, "load more" paging.
// Search text and filter live in sessionStorage so going back to the grid
// keeps them.
export function Library() {
  const [q, setQ] = useState(() => sessionStorage.getItem("q") ?? "");
  const [creator, setCreator] = useState(() => sessionStorage.getItem("creator") ?? "");
  const [creators, setCreators] = useState<Creator[]>([]);
  const [models, setModels] = useState<ModelSummary[]>([]);
  const [more, setMore] = useState(false);
  const [error, setError] = useState("");
  const seq = useRef(0);

  useEffect(() => {
    api.creators().then(setCreators, (e) => setError(String(e)));
  }, []);

  useEffect(() => {
    sessionStorage.setItem("q", q);
    sessionStorage.setItem("creator", creator);
    const mine = ++seq.current;
    const t = setTimeout(() => {
      api.models(q, creator, 0, PAGE).then(
        (ms) => {
          if (mine !== seq.current) return; // a newer search is on its way
          setModels(ms);
          setMore(ms.length === PAGE);
        },
        (e) => setError(String(e)),
      );
    }, 200);
    return () => clearTimeout(t);
  }, [q, creator]);

  const loadMore = () =>
    api.models(q, creator, models.length, PAGE).then((ms) => {
      setModels((prev) => [...prev, ...ms]);
      setMore(ms.length === PAGE);
    });

  return (
    <>
      <div className="toolbar">
        <input
          type="search"
          placeholder="Search models, creators, releases…"
          value={q}
          onChange={(e) => setQ(e.target.value)}
          autoFocus
        />
        <select value={creator} onChange={(e) => setCreator(e.target.value)}>
          <option value="">All creators</option>
          {creators.map((c) => (
            <option key={c.name} value={c.name}>
              {c.name} ({c.models})
            </option>
          ))}
        </select>
      </div>
      {error && <p className="error">{error}</p>}
      <div className="grid">
        {models.map((m) => (
          <a key={m.id} className="card" href={`#/model/${m.id}`}>
            <div className="cover">
              {m.cover ? <img src={api.imageURL(m.cover)} alt="" loading="lazy" /> : <span>no image</span>}
            </div>
            <div className="title">{m.name}</div>
            <div className="sub">
              {m.creator}
              {m.release && ` · ${m.release}`}
            </div>
            <div className="sub">
              {m.variants} variant{m.variants === 1 ? "" : "s"}
            </div>
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
