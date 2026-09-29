import { lazy, Suspense, useCallback, useEffect, useMemo, useState } from "react";
import { api, DIM_VALUES, displayName, formatBytes, formatDate, isSliced, printable, showLibrary, type DimKey, type FileRef, type Collection, type ModelDetail, type PartLink, type Tag, type Variant } from "./api";
import { SliceEditor } from "./SliceEditor";
import { TagPicker } from "./TagPicker";

// three.js is large; it loads only when a 3D view is opened.
const Viewer = lazy(() => import("./Viewer"));
const viewable = (name: string) => /\.(stl|obj|3mf)$/i.test(name);

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
  const [links, setLinks] = useState<Record<string, PartLink[]>>({});
  const [editing, setEditing] = useState<number | null>(null); // the sliced file whose contents are open
  const [linkTick, setLinkTick] = useState(0);
  useEffect(() => {
    if (!sel) return;
    api.variantSlices(sel.id).then((r) => setLinks(r.parts), () => setLinks({}));
  }, [sel?.id, linkTick]);
  const [image, setImage] = useState(0);
  const [error, setError] = useState("");
  const [viewing, setViewing] = useState<FileRef | null>(null);
  const closeViewer = useCallback(() => setViewing(null), []);

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

  // After a change: fetch again, keeping the selected variant.
  const [actionError, setActionError] = useState("");
  const refresh = () => {
    setActionError("");
    return api.model(id).then(
      (m) => {
        setM(m);
        setSel((cur) => m.variants.find((v) => v.id === cur?.id) ?? m.variants[0] ?? null);
      },
      (e) => setError(String(e)),
    );
  };
  // act runs a change and refreshes; a failure is shown, not swallowed.
  const act = (p: Promise<unknown>) => p.then(refresh, (e) => setActionError(`Could not save: ${e instanceof Error ? e.message : e}`));

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
        <a
          href="#/"
          onClick={() =>
            showLibrary({ creator: m.creator })
          }
        >
          {m.creator}
        </a>
        {m.release && <> › {m.release}</>}
        {m.category && <> › {m.category}</>}
        <button className="link hide-toggle" onClick={() => act(api.setHidden(m.id, !m.hidden))}>
          {m.hidden ? "hidden — show in library again" : "hide from library"}
        </button>
      </div>
      {actionError && <p className="error">{actionError}</p>}
      <NameEditor m={m} act={act} />
      <TagEditor m={m} act={act} />
      <CollectionEditor m={m} act={act} />
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
                {sel.relabeled && <span className="option"> (corrected)</span>}
              </h2>
              <LabelEditor variant={sel} act={act} />
              <div className="actions">
                <a className="download" href={api.zipURL(sel.id)}>
                  Download zip ({sel.parts.length} files, {formatBytes(sel.parts.reduce((n, p) => n + p.size, 0))})
                </a>
                {sel.queued ? (
                  <button onClick={() => act(api.dequeue(sel.id))}>In queue ✓ (remove)</button>
                ) : (
                  <button onClick={() => act(api.enqueue(sel.id))}>Add to print queue</button>
                )}
                <PrintButton variant={sel} act={act} />
              </div>
              {sel.prints.length > 0 && (
                <ul className="prints">
                  {sel.prints.map((p) => (
                    <li key={p.id}>
                      Printed {formatDate(p.at)}
                      {p.note && <> — {p.note}</>}
                      <button className="link" onClick={() => act(api.deletePrint(p.id))} aria-label="Delete">
                        ✕
                      </button>
                    </li>
                  ))}
                </ul>
              )}
              <ul className="parts">
                {sel.parts.map((p) => (
                  <li key={p.id} className={editing === p.id ? "open" : ""}>
                    <a href={api.partURL(p.id)}>{p.name}</a>
                    <span>
                      {links[p.id] && (
                        <span
                          className="inplates"
                          title={links[p.id].map((l) => `${l.name} ×${l.count}${l.modelName ? ` (${l.modelName})` : ""}`).join("\n")}
                        >
                          in {links[p.id].length} plate{links[p.id].length === 1 ? "" : "s"}
                        </span>
                      )}
                      {isSliced(p.name) && (
                        <button className="view3d" onClick={() => setEditing(editing === p.id ? null : p.id)}>
                          Contains…
                        </button>
                      )}
                      {printable(p.name) && <SendToPrinter part={p} />}
                      {viewable(p.name) && (
                        <button className="view3d" onClick={() => setViewing(p)}>
                          3D
                        </button>
                      )}
                      {formatBytes(p.size)}
                    </span>
                    {editing === p.id && (
                      <SliceEditor
                        part={p}
                        model={m}
                        onSaved={() => {
                          setLinkTick((n) => n + 1);
                          setEditing(null);
                        }}
                        onCancel={() => setEditing(null)}
                      />
                    )}
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
      {viewing && (
        <Suspense fallback={<div className="viewer viewer-status">Loading viewer…</div>}>
          <Viewer url={api.partURL(viewing.id)} name={viewing.name} size={viewing.size} onClose={closeViewer} />
        </Suspense>
      )}
    </div>
  );
}

type Act = (p: Promise<unknown>) => Promise<unknown>;

function NameEditor({ m, act }: { m: ModelDetail; act: Act }) {
  const [editing, setEditing] = useState(false);
  const [name, setName] = useState("");
  if (!editing) {
    return (
      <h1>
        {displayName(m)}
        <button className="link" title="Rename (the folder stays as it is)" onClick={() => (setName(displayName(m)), setEditing(true))}>
          ✎
        </button>
        {m.displayName && <span className="folder-name">folder: {m.name}</span>}
      </h1>
    );
  }
  const save = (value: string) => act(api.setName(m.id, value === m.name ? "" : value).then(() => setEditing(false)));
  return (
    <form className="name-edit" onSubmit={(e) => (e.preventDefault(), save(name.trim()))}>
      <input value={name} onChange={(e) => setName(e.target.value)} autoFocus maxLength={200} />
      <button type="submit">Save</button>
      {m.displayName && (
        <button type="button" onClick={() => save("")}>
          Use folder name
        </button>
      )}
      <button type="button" onClick={() => setEditing(false)}>
        Cancel
      </button>
    </form>
  );
}

function TagEditor({ m, act }: { m: ModelDetail; act: Act }) {
  const [existing, setExisting] = useState<Tag[]>([]);
  useEffect(() => {
    api.tags().then(setExisting, () => {});
  }, [m.tags.length]);
  const save = (tags: string[]) => act(api.setTags(m.id, tags));
  return (
    <div className="tags editable">
      {m.tags.map((t) => (
        <span key={t} className="tag">
          {t}
          <button className="link" onClick={() => save(m.tags.filter((x) => x !== t))} aria-label={`Remove ${t}`}>
            ✕
          </button>
        </span>
      ))}
      <TagPicker existing={existing} skip={m.tags} onPick={(t) => save([...m.tags, t])} />
    </div>
  );
}

// The collections a model is in; it can be in several.
function CollectionEditor({ m, act }: { m: ModelDetail; act: Act }) {
  const [mine, setMine] = useState<Collection[]>([]);
  const [all, setAll] = useState<Collection[]>([]);
  const [tick, setTick] = useState(0);
  useEffect(() => {
    api.modelCollections(m.id).then(setMine, () => {});
    api.collections().then(setAll, () => {});
  }, [m.id, tick]);
  const change = (p: Promise<unknown>) => act(p).then(() => setTick((n) => n + 1));
  const add = async (name: string) => {
    let c = all.find((x) => x.name.toLowerCase() === name.toLowerCase());
    if (!c) c = await api.createCollection(name);
    const id = c.id;
    return change(api.editCollection(id, [m.id], []));
  };
  return (
    <div className="tags editable collections-of">
      <span className="muted">Collections:</span>
      {mine.map((c) => (
        <span key={c.id} className="tag">
          <a
            href="#/"
            onClick={() => showLibrary({ collection: String(c.id) })}
          >
            {c.name}
          </a>
          <button className="link" onClick={() => change(api.editCollection(c.id, [], [m.id]))} aria-label={`Take out of ${c.name}`}>
            ✕
          </button>
        </span>
      ))}
      <TagPicker
        existing={all.map((c) => ({ tag: c.name, models: c.models }))}
        skip={mine.map((c) => c.name)}
        onPick={add}
        placeholder="add to a collection…"
        noun="collection"
      />
    </div>
  );
}

function PrintButton({ variant, act }: { variant: Variant; act: Act }) {
  const [open, setOpen] = useState(false);
  const [note, setNote] = useState("");
  if (!open) return <button onClick={() => setOpen(true)}>Mark printed</button>;
  return (
    <form
      className="print-form"
      onSubmit={(e) => {
        e.preventDefault();
        act(api.markPrinted(variant.id, note).then(() => (setOpen(false), setNote(""))));
      }}
    >
      <input value={note} onChange={(e) => setNote(e.target.value)} placeholder="note (optional)" autoFocus maxLength={500} />
      <button type="submit">Save</button>
      <button type="button" onClick={() => setOpen(false)}>
        Cancel
      </button>
    </form>
  );
}

const LABEL_KEYS: { key: DimKey; label: string }[] = [
  { key: "scale", label: "Scale" },
  { key: "supports", label: "Supports" },
  { key: "density", label: "Density" },
  { key: "format", label: "Format" },
  { key: "split", label: "Split" },
  { key: "fill", label: "Fill" },
  { key: "tech", label: "Tech" },
  { key: "extra", label: "Extra" },
];

// LabelEditor corrects what the folder names say about a variant. The
// folders stay as they are; the correction is kept by the app.
function LabelEditor({ variant, act }: { variant: Variant; act: Act }) {
  const [open, setOpen] = useState(false);
  const [dims, setDims] = useState(variant.dims);
  const [option, setOption] = useState(variant.option ?? "");
  if (!open) {
    return (
      <div className="label-links">
        <button className="link" onClick={() => (setDims(variant.dims), setOption(variant.option ?? ""), setOpen(true))}>
          fix label
        </button>
        {variant.relabeled && (
          <button className="link" onClick={() => act(api.resetLabel(variant.id))}>
            use folder names again
          </button>
        )}
      </div>
    );
  }
  return (
    <form
      className="label-edit"
      onSubmit={(e) => {
        e.preventDefault();
        act(api.setLabel(variant.id, dims, option).then(() => setOpen(false)));
      }}
    >
      {LABEL_KEYS.map(({ key, label }) => {
        const values = DIM_VALUES[key];
        const cur = dims[key] ?? "";
        return (
          <label key={key}>
            {label}
            <select value={cur} onChange={(e) => setDims({ ...dims, [key]: e.target.value || undefined })}>
              <option value="">—</option>
              {[...new Set(cur && !values.includes(cur) ? [cur, ...values] : values)].map((v) => (
                <option key={v} value={v}>
                  {v}
                </option>
              ))}
            </select>
          </label>
        );
      })}
      <label>
        Option
        <input value={option} onChange={(e) => setOption(e.target.value)} maxLength={100} placeholder="e.g. Helmet Version" />
      </label>
      <div className="label-buttons">
        <button type="submit">Save</button>
        <button type="button" onClick={() => setOpen(false)}>
          Cancel
        </button>
      </div>
    </form>
  );
}

// SendToPrinter sends a sliced file (.ctb/.goo) to the printer: upload and
// start, or only upload.
function SendToPrinter({ part }: { part: FileRef }) {
  const [open, setOpen] = useState(false);
  const [msg, setMsg] = useState("");
  const go = (start: boolean) =>
    api.sendToPrinter(part.id, start).then(
      () => (setOpen(false), setMsg(start ? "sending, then printing" : "sending")),
      (e) => setMsg(`not sent: ${e instanceof Error ? e.message : e}`),
    );
  if (msg) {
    return (
      <span className="print-msg">
        {msg} — <a href="#/printer">printer</a>
      </span>
    );
  }
  if (!open) {
    return (
      <button className="view3d" onClick={() => setOpen(true)}>
        Print
      </button>
    );
  }
  return (
    <span className="print-choice">
      <button className="small" onClick={() => go(true)}>
        Upload &amp; start
      </button>
      <button className="small" onClick={() => go(false)}>
        Upload only
      </button>
      <button className="small" onClick={() => setOpen(false)}>
        Cancel
      </button>
    </span>
  );
}
