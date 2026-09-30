import { lazy, Suspense, useCallback, useEffect, useRef, useState } from "react";
import { api, displayName, formatBytes, type Creator, type ModelDetail } from "./api";

const Viewer = lazy(() => import("./Viewer"));
const viewable = (name: string) => /\.(stl|obj|3mf)$/i.test(name);

// Going through the models whose preview is a render, one after the other: the current picture on the left, the 3D view
// on the right. "Use this view" replaces the picture, "Skip" keeps it. Either way the model is done and does not come up
// again (Back takes the last one back). Models that already got a chosen picture are not in the queue.
export function PreviewReview() {
  const [creator, setCreator] = useState(() => {
    try {
      return sessionStorage.getItem("reviewCreator") ?? "";
    } catch {
      return "";
    }
  });
  const [creators, setCreators] = useState<Creator[]>([]);
  const [counts, setCounts] = useState({ remaining: 0, done: 0 });
  const [model, setModel] = useState<ModelDetail | null>(null);
  const [loading, setLoading] = useState(true);
  const [history, setHistory] = useState<number[]>([]);
  const [note, setNote] = useState("");
  const [error, setError] = useState("");
  const [pick, setPick] = useState<number | null>(null);
  const busy = useRef(false);

  useEffect(() => {
    api.creators().then(setCreators, () => {});
  }, []);

  const show = useCallback(
    async (c: string) => {
      setLoading(true);
      try {
        const st = await api.reviewState(c, 3);
        setCounts({ remaining: st.remaining, done: st.done });
        setModel(st.next.length ? await api.model(st.next[0]) : null);
        setPick(null);
        setError("");
      } catch (e) {
        setError(String(e));
      } finally {
        setLoading(false);
      }
    },
    [],
  );
  useEffect(() => {
    setHistory([]);
    setNote("");
    try {
      sessionStorage.setItem("reviewCreator", creator);
    } catch {
      /* no storage */
    }
    void show(creator);
  }, [creator, show]);

  // finish the current model (something was chosen or skipped) and go on to the next
  const finish = useCallback(
    async (action: () => Promise<unknown>, message: string) => {
      if (!model || busy.current) return;
      busy.current = true;
      try {
        await action();
        setHistory((h) => [...h, model.id]);
        setNote(`${message}: ${displayName(model)}`);
        await show(creator);
      } catch (e) {
        setError(String(e));
      } finally {
        busy.current = false;
      }
    },
    [model, creator, show],
  );
  const skip = useCallback(() => finish(() => api.skipReview(model!.id), "Kept"), [finish, model]);
  const back = useCallback(async () => {
    const last = history[history.length - 1];
    if (last === undefined || busy.current) return;
    busy.current = true;
    try {
      await api.undoReview(last);
      setHistory((h) => h.slice(0, -1));
      setNote("Went back one model.");
      await show(creator);
    } catch (e) {
      setError(String(e));
    } finally {
      busy.current = false;
    }
  }, [history, creator, show]);

  useEffect(() => {
    const on = (e: KeyboardEvent) => {
      const t = e.target as HTMLElement | null;
      if (e.ctrlKey || e.metaKey || e.altKey || (t && /^(INPUT|TEXTAREA|SELECT)$/.test(t.tagName))) return;
      if (e.key === "s" || e.key === "ArrowRight") {
        e.preventDefault();
        void skip();
      } else if (e.key === "b" || e.key === "ArrowLeft") {
        e.preventDefault();
        void back();
      }
    };
    addEventListener("keydown", on);
    return () => removeEventListener("keydown", on);
  }, [skip, back]);

  const parts = model
    ? model.variants.flatMap((v) => v.parts.filter((p) => viewable(p.name)).map((p) => ({ p, label: v.label || "files", supported: /^supported/i.test(v.dims.supports ?? "") })))
    : [];
  // the picture that comes first: the largest file without supports (supports hide the shape), STL before other formats
  const first = [...parts].sort(
    (a, b) => Number(a.supported) - Number(b.supported) || Number(/\.stl$/i.test(b.p.name)) - Number(/\.stl$/i.test(a.p.name)) || b.p.size - a.p.size,
  )[0];
  const part = parts.find(({ p }) => p.id === pick) ?? first;
  const total = counts.remaining + counts.done;

  return (
    <div className="review">
      <h1>Fix preview pictures</h1>
      <p className="muted">
        These models show a picture the library rendered itself. Turn the 3D view until it looks right and use it, or skip the model if the
        picture is fine. Every model you handle is marked done and does not come up again.
      </p>
      <div className="review-bar">
        <select value={creator} onChange={(e) => setCreator(e.target.value)} aria-label="Creator">
          <option value="">All creators</option>
          {creators.map((c) => (
            <option key={c.name} value={c.name}>
              {c.name}
            </option>
          ))}
        </select>
        <progress value={counts.done} max={Math.max(1, total)} />
        <span className="muted">
          {counts.remaining.toLocaleString()} left · {counts.done.toLocaleString()} done
        </span>
        {counts.done > 0 && (
          <button
            className="link"
            onClick={() => {
              if (confirm("Put the models you skipped back into the review? Pictures you chose stay as they are."))
                api.resetReview(creator).then((r) => (setNote(`${r.reset} skipped model${r.reset === 1 ? "" : "s"} back in the review.`), show(creator)), (e) => setError(String(e)));
            }}
          >
            review the skipped ones again
          </button>
        )}
      </div>
      {error && <p className="error">{error}</p>}
      {note && <p className="muted">{note}</p>}
      {!model && !loading && <p className="empty">{counts.done > 0 ? "All done — nothing left to review." : "No models with a rendered preview."}</p>}
      {model && (
        <>
          <div className="review-title">
            <strong>{displayName(model)}</strong>
            <span className="muted">
              {model.creator}
              {model.release ? ` › ${model.release}` : ""}
            </span>
            <a href={`#/model/${model.id}`} target="_blank" rel="noreferrer">
              open the model page
            </a>
          </div>
          <div className="review-body">
            <div className="review-current">
              <h2>Current picture</h2>
              <img src={api.previewURL(model.id, model.previewVersion)} alt={`Current preview of ${displayName(model)}`} />
            </div>
            <div className="review-3d">
              <h2>3D view</h2>
              {part ? (
                <>
                  <select value={part.p.id} onChange={(e) => setPick(Number(e.target.value))} aria-label="Part to view">
                    {parts.map(({ p, label }) => (
                      <option key={p.id} value={p.id}>
                        {label} · {p.name} ({formatBytes(p.size)})
                      </option>
                    ))}
                  </select>
                  <Suspense fallback={<div className="viewer inline viewer-status">Loading viewer…</div>}>
                    <Viewer
                      key={`${model.id}-${part.p.id}`}
                      url={api.partURL(part.p.id)}
                      name={part.p.name}
                      size={part.p.size}
                      inline
                      hotkey
                      onSetPreview={(png) => finish(() => api.setPreview(model.id, png), "New picture saved")}
                    />
                  </Suspense>
                </>
              ) : (
                <p className="muted">No file the viewer can open.</p>
              )}
            </div>
          </div>
          <div className="actions review-actions">
            <button onClick={back} disabled={history.length === 0}>
              ← Back (B)
            </button>
            <button onClick={skip}>Skip, keep the current picture (S)</button>
            <span className="muted">Enter: use this view · S / → skip · B / ← back</span>
          </div>
        </>
      )}
    </div>
  );
}
