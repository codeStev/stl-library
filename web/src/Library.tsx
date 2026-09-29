import { useEffect, useRef, useState } from "react";
import { TagPicker } from "./TagPicker";
import { api, displayName, type Creator, type Filters, type ModelSummary, type Tag } from "./api";

const PAGE = 60;
const empty: Filters = { q: "", creator: "", tag: "", printed: "", hidden: "" };

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
  const [selecting, setSelecting] = useState(false);
  const [picked, setPicked] = useState<Set<number>>(new Set());
  const [note, setNote] = useState("");
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

  const toggle = (id: number) =>
    setPicked((prev) => {
      const next = new Set(prev);
      next.has(id) ? next.delete(id) : next.add(id);
      return next;
    });
  const stopSelecting = () => {
    setSelecting(false);
    setPicked(new Set());
    setNote("");
  };
  // Tags on the selected models, so one can be taken off all of them.
  const pickedTags = [...new Set(models.filter((m) => picked.has(m.id)).flatMap((m) => m.tags))].sort();
  const edit = (add: string[], remove: string[]) =>
    api.editTags([...picked], add, remove).then(
      () => {
        setNote(`${add.length ? `Added “${add.join("”, “")}”` : `Removed “${remove.join("”, “")}”`} on ${picked.size} model${picked.size === 1 ? "" : "s"}`);
        setError("");
        api.tags().then(setTags, () => {});
        return api.models(f, 0, Math.max(models.length, PAGE)).then(setModels);
      },
      (e) => setError(String(e)),
    );

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
        <button className={`chip${selecting ? " active" : ""}`} onClick={() => (selecting ? stopSelecting() : setSelecting(true))}>
          {selecting ? "Done selecting" : "Select models"}
        </button>
        <label className="check">
          <input type="checkbox" checked={f.hidden === "yes"} onChange={(e) => set({ hidden: e.target.checked ? "yes" : "" })} />
          show hidden
        </label>
      </div>
      {error && <p className="error">{error}</p>}
      {selecting && (
        <div className="bulkbar">
          <strong>{picked.size} selected</strong>
          <button className="link" onClick={() => setPicked(new Set(models.map((m) => m.id)))}>
            all {models.length} shown
          </button>
          <button className="link" onClick={() => setPicked(new Set())} disabled={picked.size === 0}>
            none
          </button>
          {picked.size > 0 && (
            <>
              <span>Add tag</span>
              <TagPicker existing={tags} onPick={(t) => edit([t], [])} placeholder="pick or create a tag…" />
              {pickedTags.length > 0 && (
                <>
                  <span>Remove tag</span>
                  <select value="" onChange={(e) => e.target.value && edit([], [e.target.value])}>
                    <option value="">choose…</option>
                    {pickedTags.map((t) => (
                      <option key={t} value={t}>
                        {t}
                      </option>
                    ))}
                  </select>
                </>
              )}
            </>
          )}
          {note && <span className="note">{note}</span>}
        </div>
      )}
      <div className="grid">
        {models.map((m) => (
          <a
            key={m.id}
            className={`card${m.hidden ? " hidden-model" : ""}${picked.has(m.id) ? " picked" : ""}`}
            href={`#/model/${m.id}`}
            onClick={(e) => {
              if (selecting) {
                e.preventDefault();
                toggle(m.id);
              }
            }}
          >
            <div className="cover">
              {selecting && <span className={`tick${picked.has(m.id) ? " on" : ""}`}>{picked.has(m.id) ? "✓" : ""}</span>}
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
