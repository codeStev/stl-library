import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { TagPicker } from "./TagPicker";
import { api, displayName, type Collection, type Creator, type Filters, type ModelSummary, type Tag } from "./api";

const PAGE = 60;
const empty: Filters = { q: "", creator: "", tag: "", collection: "", printed: "", hidden: "" };

function loadFilters(): Filters {
  try {
    return { ...empty, ...JSON.parse(sessionStorage.getItem("filters") ?? "{}") };
  } catch {
    return empty;
  }
}

// Opening a model unmounts the grid. What was loaded and where the page was
// scrolled is kept here, so "back" lands where the user left: the same
// models (all the pages loaded so far) at the same scroll position.
let remembered: { key: string; models: ModelSummary[]; more: boolean; scrollY: number } | null = null;

// The library grid: search, filters (creator, tag, printed), previews,
// "load more" paging. Filters live in sessionStorage so going back to the
// grid keeps them.
export function Library() {
  const [f, setF] = useState<Filters>(loadFilters);
  const [creators, setCreators] = useState<Creator[]>([]);
  const [tags, setTags] = useState<Tag[]>([]);
  const [collections, setCollections] = useState<Collection[]>([]);
  const back = remembered && remembered.key === JSON.stringify(loadFilters()) ? remembered : null;
  const [models, setModels] = useState<ModelSummary[]>(back?.models ?? []);
  const [more, setMore] = useState(back?.more ?? false);
  const restore = useRef<number | null>(back ? back.scrollY : null); // scroll position to put back
  const comingBack = useRef(back !== null); // the first load after "back" refreshes what was shown
  const latest = useRef({ f, models, more });
  latest.current = { f, models, more };
  const [error, setError] = useState("");
  const [selecting, setSelecting] = useState(false);
  const [picked, setPicked] = useState<Set<number>>(new Set());
  const [note, setNote] = useState("");
  const seq = useRef(0);
  const set = (patch: Partial<Filters>) => setF((prev) => ({ ...prev, ...patch }));

  useEffect(() => {
    api.creators().then(setCreators, (e) => setError(String(e)));
    api.tags().then(setTags, () => {});
    api.collections().then(setCollections, () => {});
  }, []);

  useEffect(() => {
    sessionStorage.setItem("filters", JSON.stringify(f));
    const mine = ++seq.current;
    // Coming back with the same filters: refresh as many models as were shown.
    // Coming back: refresh as many models as were shown, so the page keeps its length.
    // The server returns at most 500 at once: beyond that the remembered list stays as it is.
    const back = comingBack.current;
    comingBack.current = false;
    if (back && models.length > 500) return;
    const keep = back ? Math.min(Math.max(models.length, PAGE), 500) : PAGE;
    const t = setTimeout(() => {
      api.models(f, 0, keep).then(
        (ms) => {
          if (mine !== seq.current) return; // a newer search is on its way
          setModels(ms);
          setMore(ms.length === keep);
        },
        (e) => setError(String(e)),
      );
    }, back ? 0 : 200);
    return () => clearTimeout(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [f]);

  // Put the page back where it was once the remembered models are on screen.
  useLayoutEffect(() => {
    if (restore.current !== null && models.length > 0) {
      window.scrollTo(0, restore.current);
      restore.current = null;
    }
  }, [models]);

  // Remember the state when leaving the grid.
  useEffect(
    () => () => {
      const { f, models, more } = latest.current;
      remembered = { key: JSON.stringify(f), models, more, scrollY: window.scrollY };
    },
    [],
  );

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

  // Put the selected models into a collection (made first when it is new), or take them out of the one being viewed.
  const toCollection = async (name: string) => {
    try {
      let c = collections.find((x) => x.name.toLowerCase() === name.toLowerCase());
      if (!c) c = await api.createCollection(name);
      await api.editCollection(c.id, [...picked], []);
      setNote(`Added ${picked.size} model${picked.size === 1 ? "" : "s"} to “${c.name}”`);
      setError("");
      api.collections().then(setCollections, () => {});
    } catch (e) {
      setError(String(e));
    }
  };
  const fromCollection = () =>
    api.editCollection(Number(f.collection), [], [...picked]).then(
      () => {
        setNote(`Removed ${picked.size} model${picked.size === 1 ? "" : "s"} from this collection`);
        setPicked(new Set());
        api.collections().then(setCollections, () => {});
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
        {collections.length > 0 && (
          <select value={f.collection} onChange={(e) => set({ collection: e.target.value })}>
            <option value="">All collections</option>
            {collections.map((c) => (
              <option key={c.id} value={c.id}>
                {c.name} ({c.models})
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
              <span>Collection</span>
              <TagPicker
                existing={collections.map((c) => ({ tag: c.name, models: c.models }))}
                onPick={toCollection}
                placeholder="add to a collection…"
                noun="collection"
              />
              {f.collection !== "" && (
                <button className="link" onClick={fromCollection}>
                  remove from this collection
                </button>
              )}
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
