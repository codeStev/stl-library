import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { TagPicker } from "./TagPicker";
import { api, displayName, emptyFilters, formatDate, type Collection, type Creator, type Facet, type Filters, type ModelSummary, type Tag } from "./api";

const PAGE = 60;
const empty: Filters = emptyFilters;

function loadFilters(): Filters {
  try {
    return { ...empty, ...JSON.parse(sessionStorage.getItem("filters") ?? "{}") };
  } catch {
    return empty;
  }
}

function loadPage(): number {
  const n = Number(sessionStorage.getItem("libpage"));
  return Number.isInteger(n) && n > 0 ? n : 0;
}

// The library grid: search, filters (creator, tag, collection, printed),
// previews and numbered pages. Filters, page and scroll position live in
// sessionStorage, so going back from a model lands on the same page at the
// same place.
export function Library() {
  const [f, setF] = useState<Filters>(loadFilters);
  const [page, setPage] = useState(loadPage);
  const [creators, setCreators] = useState<Creator[]>([]);
  const [tags, setTags] = useState<Tag[]>([]);
  const [collections, setCollections] = useState<Collection[]>([]);
  const [facets, setFacets] = useState<Record<"scale" | "supports" | "format" | "fill", Facet[]> | null>(null);
  const [more, setMore] = useState(false); // the extra filters are open
  const [models, setModels] = useState<ModelSummary[]>([]);
  const [total, setTotal] = useState(0);
  // The scroll position to put back once the page it belongs to has loaded.
  const restore = useRef<{ key: string; y: number } | null>(
    (() => {
      try {
        return JSON.parse(sessionStorage.getItem("libscroll") ?? "null");
      } catch {
        return null;
      }
    })(),
  );
  const [error, setError] = useState("");
  const [selecting, setSelecting] = useState(false);
  const [picked, setPicked] = useState<Set<number>>(new Set());
  const [note, setNote] = useState("");
  // A changed filter starts at the first page.
  const set = (patch: Partial<Filters>) => {
    setF((prev) => ({ ...prev, ...patch }));
    setPage(0);
  };
  const filtersInUse = [f.scale, f.supports, f.format, f.fill, f.plate, f.added].filter(Boolean).length;
  const pages = Math.max(1, Math.ceil(total / PAGE));
  const goto = (n: number) => {
    setPage(Math.min(Math.max(n, 0), pages - 1));
    window.scrollTo(0, 0);
  };

  useEffect(() => {
    api.creators().then(setCreators, (e) => setError(String(e)));
    api.tags().then(setTags, () => {});
    api.collections().then(setCollections, () => {});
    api.facets().then(setFacets, () => {});
  }, []);

  const load = () =>
    api.modelsPage(f, page * PAGE, PAGE).then(
      (r) => {
        setModels(r.items);
        setTotal(r.total);
        if (r.items.length === 0 && r.total > 0) setPage(Math.max(0, Math.ceil(r.total / PAGE) - 1)); // the page is gone (fewer results now)
      },
      (e) => setError(String(e)),
    );

  const seq = useRef(0);
  useEffect(() => {
    sessionStorage.setItem("filters", JSON.stringify(f));
    sessionStorage.setItem("libpage", String(page));
    const mine = ++seq.current;
    const t = setTimeout(() => {
      api.modelsPage(f, page * PAGE, PAGE).then(
        (r) => {
          if (mine !== seq.current) return; // a newer search is on its way
          setModels(r.items);
          setTotal(r.total);
          if (r.items.length === 0 && r.total > 0) setPage(Math.max(0, Math.ceil(r.total / PAGE) - 1));
        },
        (e) => setError(String(e)),
      );
    }, 150);
    return () => clearTimeout(t);
  }, [f, page]);

  // Put the page back where it was, once, when the page it was scrolled on has loaded.
  const key = JSON.stringify([f, page]);
  useLayoutEffect(() => {
    if (restore.current && models.length > 0) {
      if (restore.current.key === key) window.scrollTo(0, restore.current.y);
      restore.current = null;
    }
  }, [models]);

  // Remember the scroll position for coming back. It is saved the moment the page is left (the
  // address changes, before anything is drawn): later the new page has reset the scrolling already.
  useEffect(() => {
    const save = () => sessionStorage.setItem("libscroll", JSON.stringify({ key: latestKey.current, y: window.scrollY }));
    addEventListener("hashchange", save);
    addEventListener("pagehide", save);
    return () => {
      removeEventListener("hashchange", save);
      removeEventListener("pagehide", save);
    };
  }, []);
  const latestKey = useRef(key);
  latestKey.current = key;

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
        return load();
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
        return load();
      },
      (e) => setError(String(e)),
    );

  // Select every model of the search, not only this page.
  const selectAllResults = async () => {
    try {
      const ids: number[] = [];
      for (let off = 0; off < total; off += 500) ids.push(...(await api.modelsPage(f, off, 500)).items.map((m) => m.id));
      setPicked(new Set(ids));
    } catch (e) {
      setError(String(e));
    }
  };

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
        <select value={f.sort} onChange={(e) => set({ sort: e.target.value as Filters["sort"] })} aria-label="Sort">
          <option value="">Sort: name / relevance</option>
          <option value="added">Sort: newest first</option>
          <option value="printed">Sort: last printed</option>
        </select>
        <button className={`chip${more || filtersInUse ? " active" : ""}`} onClick={() => setMore(!more)}>
          Filters{filtersInUse ? ` (${filtersInUse})` : ""}
        </button>
        <button className={`chip${selecting ? " active" : ""}`} onClick={() => (selecting ? stopSelecting() : setSelecting(true))}>
          {selecting ? "Done selecting" : "Select models"}
        </button>
        <label className="check">
          <input type="checkbox" checked={f.hidden === "yes"} onChange={(e) => set({ hidden: e.target.checked ? "yes" : "" })} />
          show hidden
        </label>
      </div>
      {(more || filtersInUse > 0) && (
        <div className="toolbar filters">
          {(["scale", "supports", "format", "fill"] as const).map((dim) => (
            <select key={dim} value={f[dim]} onChange={(e) => set({ [dim]: e.target.value } as Partial<Filters>)} aria-label={dim}>
              <option value="">Any {dim}</option>
              {(facets?.[dim] ?? []).map((x) => (
                <option key={x.value} value={x.value}>
                  {x.value} ({x.models})
                </option>
              ))}
            </select>
          ))}
          <label className="check">
            <input type="checkbox" checked={f.plate === "yes"} onChange={(e) => set({ plate: e.target.checked ? "yes" : "" })} />
            has a sliced file
          </label>
          <label className="check">
            <input type="checkbox" checked={f.added === "14"} onChange={(e) => set({ added: e.target.checked ? "14" : "" })} />
            new (last 14 days)
          </label>
          {filtersInUse > 0 && (
            <button className="link" onClick={() => set({ scale: "", supports: "", format: "", fill: "", plate: "", added: "" })}>
              clear
            </button>
          )}
        </div>
      )}
      {error && <p className="error">{error}</p>}
      <div className="resultbar">
        <span className="muted">
          {total.toLocaleString()} model{total === 1 ? "" : "s"}
          {pages > 1 && ` · page ${page + 1} of ${pages}`}
        </span>
        <Pager page={page} pages={pages} onPage={goto} />
      </div>
      {selecting && (
        <div className="bulkbar">
          <strong>{picked.size} selected</strong>
          <button className="link" onClick={() => setPicked(new Set(models.map((m) => m.id)))}>
            this page ({models.length})
          </button>
          {total > models.length && (
            <button className="link" onClick={selectAllResults}>
              all {total} results
            </button>
          )}
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
              {m.preview ? <img src={api.previewURL(m.id, m.previewVersion)} alt="" loading="lazy" /> : <span>no preview</span>}
              {m.prints > 0 && <span className="badge">printed{m.prints > 1 ? ` ×${m.prints}` : ""}</span>}
              {m.addedUnix && Date.now() / 1000 - m.addedUnix < 14 * 86400 && <span className="badge new">new</span>}
            </div>
            <div className="title">{displayName(m)}</div>
            <div className="sub">
              {m.creator}
              {m.release && ` · ${m.release}`}
            </div>
            <div className="sub">
              {m.variants} variant{m.variants === 1 ? "" : "s"}
              {m.lastPrintedUnix ? ` · printed ${formatDate(m.lastPrintedUnix)}` : ""}
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
      <Pager page={page} pages={pages} onPage={goto} />
    </>
  );
}

// Pager shows numbered pages: the first, the last and a few around the
// current one, with gaps as "…".
function Pager({ page, pages, onPage }: { page: number; pages: number; onPage: (n: number) => void }) {
  if (pages <= 1) return null;
  const shown = [...new Set([0, 1, page - 2, page - 1, page, page + 1, page + 2, pages - 2, pages - 1])]
    .filter((n) => n >= 0 && n < pages)
    .sort((a, b) => a - b);
  const items: (number | "gap")[] = [];
  shown.forEach((n, i) => {
    if (i > 0 && n - shown[i - 1] > 1) items.push("gap");
    items.push(n);
  });
  return (
    <nav className="pager" aria-label="Pages">
      <button className="chip" disabled={page === 0} onClick={() => onPage(page - 1)} aria-label="Previous page">
        ‹
      </button>
      {items.map((n, i) =>
        n === "gap" ? (
          <span key={`g${i}`} className="muted">
            …
          </span>
        ) : (
          <button key={n} className={`chip${n === page ? " active" : ""}`} onClick={() => onPage(n)} aria-current={n === page ? "page" : undefined}>
            {n + 1}
          </button>
        ),
      )}
      <button className="chip" disabled={page === pages - 1} onClick={() => onPage(page + 1)} aria-label="Next page">
        ›
      </button>
    </nav>
  );
}
