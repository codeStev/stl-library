import { useEffect, useMemo, useState } from "react";
import { api, formatDate, type ImportRecord } from "./api";

const ORDER: ImportRecord["state"][] = ["failed", "queued", "waiting", "imported", "existing"];
const TITLES: Record<ImportRecord["state"], string> = {
  failed: "Need attention",
  queued: "Requested",
  waiting: "Waiting (download not complete yet)",
  imported: "Imported",
  existing: "Were there before importing started (import on request)",
};

// What the importer did with each download folder.
export function Imports({ admin = true }: { admin?: boolean }) {
  const [data, setData] = useState<{ enabled: boolean; records: ImportRecord[] } | null>(null);
  const [error, setError] = useState("");
  const reload = () => api.imports().then(setData, (e) => setError(String(e)));
  useEffect(() => {
    reload();
  }, []);
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
      {groups.length === 0 && <p className="empty">Nothing seen in the downloads folder yet.</p>}
      {groups.map(([state, list]) => (
        <details key={state} open={state !== "existing" && state !== "imported"}>
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
