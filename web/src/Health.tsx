import { useCallback, useEffect, useState } from "react";
import { api, formatBytes, formatDate, type DuplicateGroup, type HealthState } from "./api";

// The health page: how far the background hashing is, files that are in the library twice (same content),
// files whose content changed on their own, and files that are gone.
export function Health({ admin }: { admin: boolean }) {
  const [h, setH] = useState<HealthState | null>(null);
  const [error, setError] = useState("");
  const load = useCallback(() => api.health().then(setH, (e) => setError(String(e))), []);
  useEffect(() => {
    load();
    const t = setInterval(load, 5000); // the hashing progresses while the page is open
    return () => clearInterval(t);
  }, [load]);
  if (!h) return <p>{error || "Loading…"}</p>;
  const pct = h.files ? Math.round((h.hashed / h.files) * 100) : 100;
  const act = (p: Promise<unknown>) => p.then(load, (e) => setError(String(e)));
  return (
    <div className="health">
      <h1>Library health</h1>
      {error && <p className="error">{error}</p>}
      <section>
        <h2>Checking the files</h2>
        <p className="muted">
          Every file is read once (at low priority, in the background) to tell identical files apart. Later the files are read again now and then to spot
          damage that happened on its own. The files are never changed.
        </p>
        <div className="print-progress">
          <progress value={h.hashed} max={h.files || 1} />
          <span>
            {h.hashed.toLocaleString()} of {h.files.toLocaleString()} files checked ({pct}%)
            {h.running && !h.paused && h.current ? " · working…" : h.paused ? " · paused" : ""}
          </span>
        </div>
        {h.errors > 0 && <p className="muted">{h.errors} file(s) could not be read in this run; they are tried again later.</p>}
        {admin && h.enabled && (
          <div className="actions">
            {h.paused ? <button onClick={() => act(api.healthRun())}>Start checking</button> : <button onClick={() => act(api.healthPause())}>Pause</button>}
          </div>
        )}
      </section>

      <section>
        <h2>Damaged files ({h.corrupt.length})</h2>
        {h.corrupt.length === 0 ? (
          <p className="muted">None found.</p>
        ) : (
          <ul className="job-items">
            {h.corrupt.map((e) => (
              <li key={e.id}>
                <span className="name">{e.path}</span>
                <span className="muted">
                  {e.detail}
                  {e.atUnix ? ` (${formatDate(e.atUnix)})` : ""}
                </span>
                {admin && e.id && (
                  <button className="link" onClick={() => act(api.dismissHealthEvent(e.id as number))}>
                    dismiss
                  </button>
                )}
              </li>
            ))}
          </ul>
        )}
      </section>

      <section>
        <h2>Files that are gone ({h.missing.length})</h2>
        {h.missing.length === 0 ? (
          <p className="muted">None. (A file that only moved is not reported.)</p>
        ) : (
          <ul className="job-items">
            {h.missing.map((e) => (
              <li key={e.path}>
                <span className="name">{e.path}</span>
              </li>
            ))}
          </ul>
        )}
      </section>

      <Duplicates />
    </div>
  );
}

function Duplicates() {
  const [min, setMin] = useState(64);
  const [groups, setGroups] = useState<DuplicateGroup[]>([]);
  const [total, setTotal] = useState(0);
  const [error, setError] = useState("");
  const load = useCallback(
    (offset: number) =>
      api.duplicates(min, offset).then(
        (p) => {
          setTotal(p.total);
          setGroups((prev) => (offset === 0 ? p.groups : [...prev, ...p.groups]));
        },
        (e) => setError(String(e)),
      ),
    [min],
  );
  useEffect(() => {
    load(0);
  }, [load]);
  return (
    <section>
      <h2>Files that are in the library twice ({total})</h2>
      <p className="muted">
        Files with exactly the same content. Nothing is deleted here: look at where each copy lives and remove the extra ones yourself.{" "}
        Smallest file considered:{" "}
        <select value={min} onChange={(e) => setMin(Number(e.target.value))}>
          {[0, 64, 1024, 10240].map((k) => (
            <option key={k} value={k}>
              {k === 0 ? "any size" : k >= 1024 ? `${k / 1024} MB` : `${k} KB`}
            </option>
          ))}
        </select>
      </p>
      {error && <p className="error">{error}</p>}
      {groups.map((g) => (
        <div key={g.sha256 + g.size} className="dup-group">
          <div className="dup-head">
            {g.files.length} copies of a {formatBytes(g.size)} file · <strong>{formatBytes(g.wasted)}</strong> wasted
          </div>
          <ul className="job-items">
            {g.files.map((f) => (
              <li key={f.partId}>
                <a href={`#/model/${f.modelId}`}>{f.modelName}</a>
                <span className="name muted">{f.path}</span>
              </li>
            ))}
          </ul>
        </div>
      ))}
      {groups.length < total && (
        <button className="more" onClick={() => load(groups.length)}>
          Show more
        </button>
      )}
    </section>
  );
}
