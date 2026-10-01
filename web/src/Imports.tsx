import { useEffect, useMemo, useState } from "react";
import { api, formatDate, type ImportList, type ImportPreview, type ImportRecord } from "./api";

const ORDER: ImportRecord["state"][] = ["failed", "queued", "waiting", "imported", "duplicate", "existing"];
const TITLES: Record<ImportRecord["state"], string> = {
  failed: "Need attention",
  queued: "Requested",
  waiting: "Waiting (download not complete yet)",
  imported: "Imported",
  duplicate: "Skipped (already in the library, import on request)",
  existing: "Were there before importing started (import on request)",
};

// What the importer did with each download folder.
export function Imports({ admin = true }: { admin?: boolean }) {
  const [data, setData] = useState<ImportList | null>(null);
  const [error, setError] = useState("");
  const [preview, setPreview] = useState<{ source: string; data?: ImportPreview; error?: string } | null>(null);
  const reload = () => api.imports().then(setData, (e) => setError(String(e)));
  useEffect(() => {
    reload();
  }, []);
  // While a run is going (or was just asked for), keep the page current.
  const running = data?.running ?? false;
  useEffect(() => {
    if (!running) return;
    const t = setInterval(reload, 3000);
    return () => clearInterval(t);
  }, [running]);
  const act = (p: Promise<unknown>) => p.then(() => setTimeout(reload, 500), (e) => setError(String(e)));
  const showPreview = (source: string) => {
    setPreview({ source });
    api.previewImport(source).then(
      (d) => setPreview({ source, data: d }),
      (e) => setPreview({ source, error: String(e) }),
    );
  };
  const skipped = data?.records.filter((r) => r.state === "duplicate" && !r.message?.includes("removed from the downloads")).length ?? 0;
  const failed = data?.records.filter((r) => r.state === "failed").length ?? 0;
  const groups = useMemo(() => {
    const out = new Map<string, ImportRecord[]>();
    for (const r of data?.records ?? []) out.set(r.state, [...(out.get(r.state) ?? []), r]);
    return ORDER.filter((s) => out.has(s)).map((s) => [s, out.get(s)!] as const);
  }, [data]);

  if (error) return <p className="error">{error}</p>;
  if (!data) return <p>Loading…</p>;
  if (!data.enabled) {
    return (
      <p className="empty">
        Importing is off. Set <code>IMPORT_SOURCE</code> to your downloads folder (and mount the library writable) to
        copy new, fully downloaded models into the library automatically.
      </p>
    );
  }
  return (
    <div className="imports">
      <div className="actions">
        {running ? (
          <span className="progress">
            Importing{data.current ? <> <code>{data.current}</code></> : "…"} · {data.done} folder{data.done === 1 ? "" : "s"} done
          </span>
        ) : (
          <span className="muted">Idle. New downloads are picked up automatically.</span>
        )}
        {admin && (
          <>
            <button className="small" disabled={running} onClick={() => act(api.runImport())}>
              Import now
            </button>
            {skipped > 0 && (
              <button
                className="small"
                title="Removes the skipped folders from the downloads after checking again that the library has all their model files"
                onClick={() =>
                  window.confirm(`Delete ${skipped} skipped download folder(s) from the downloads folder? Their model files are in the library already.`) &&
                  act(api.cleanDuplicateImports())
                }
              >
                Delete skipped duplicates from the downloads ({skipped})
              </button>
            )}
            {failed > 0 && (
              <button className="small" onClick={() => act(api.retryFailedImports())}>
                Retry all failed ({failed})
              </button>
            )}
          </>
        )}
      </div>
      {groups.length === 0 && <p className="empty">Nothing seen in the downloads folder yet.</p>}
      {groups.map(([state, list]) => (
        <details key={state} open={state !== "existing" && state !== "imported" && state !== "duplicate"}>
          <summary>
            {TITLES[state]} <span className="count">{list.length}</span>
          </summary>
          <ul>
            {list.map((r) => (
              <li key={r.source}>
                <div>
                  <code>{r.source}</code>
                  {r.target && <span className="reason"> → {r.target}</span>}
                </div>
                <span className="reason">
                  {r.message}
                  {r.files > 0 && ` · ${r.files} files`} · {formatDate(r.updated)}
                </span>
                {admin && state !== "imported" && (
                  <button className="small" onClick={() => (preview?.source === r.source ? setPreview(null) : showPreview(r.source))}>
                    {preview?.source === r.source ? "Hide preview" : "Preview"}
                  </button>
                )}
                {preview?.source === r.source && (
                  <div className="preview">
                    {preview.error && <p className="error">{preview.error}</p>}
                    {!preview.error && !preview.data && <p className="muted">Looking…</p>}
                    {preview.data && <PreviewList p={preview.data} />}
                  </div>
                )}
                {admin && (state === "existing" || state === "failed") && (
                  <button className="small" onClick={() => api.requestImport(r.source).then(reload, (e) => setError(String(e)))}>
                    Import
                  </button>
                )}
              </li>
            ))}
          </ul>
        </details>
      ))}
    </div>
  );
}

function PreviewList({ p }: { p: ImportPreview }) {
  if (p.error) return <p className="error">Cannot read it yet: {p.error}</p>;
  return (
    <>
      <p className="muted">
        {p.total} file{p.total === 1 ? "" : "s"} would go to <code>{p.target ?? "?"}</code>
        {!p.settled && p.why ? ` (not imported yet: ${p.why})` : ""}. Nothing is written.
      </p>
      <ul className="placements">
        {p.placements.map((x) => (
          <li key={x.from}>
            <code>{x.from}</code> → <code>{x.to}</code>
          </li>
        ))}
      </ul>
      {p.total > p.placements.length && <p className="muted">… and {p.total - p.placements.length} more</p>}
    </>
  );
}
