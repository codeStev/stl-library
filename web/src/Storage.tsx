import { useEffect, useState } from "react";
import { api, formatBytes, type StorageReport } from "./api";

// Where the library's space goes: per creator (and release), the biggest models, the kind of file, and what identical files waste.
export function Storage() {
  const [r, setR] = useState<StorageReport | null>(null);
  const [error, setError] = useState("");
  useEffect(() => {
    api.storage().then(setR, (e) => setError(String(e)));
  }, []);
  if (error) return <p className="error">{error}</p>;
  if (!r) return <p className="muted">Loading…</p>;
  const pct = (n: number) => (r.bytes ? Math.max(1, Math.round((n / r.bytes) * 100)) : 0);
  return (
    <section className="storage">
      <h2>Storage</h2>
      <p className="muted">
        {formatBytes(r.bytes)} in {r.models.toLocaleString()} models ({r.files.toLocaleString()} files).
        {r.duplicateBytes > 0 && (
          <>
            {" "}
            Identical files waste <strong>{formatBytes(r.duplicateBytes)}</strong> in {r.duplicateGroups.toLocaleString()} groups
            {r.hashed < r.files ? ` (so far: ${Math.round((r.hashed / r.files) * 100)}% of the files are checked)` : ""}.
          </>
        )}
      </p>
      <div className="storage-cols">
        <div>
          <h3>By creator</h3>
          <ul className="bars">
            {r.creators.map((c) => (
              <li key={c.name}>
                <details>
                  <summary>
                    <span className="bar" style={{ width: `${pct(c.bytes)}%` }} />
                    <span className="label">{c.name}</span>
                    <span className="muted">
                      {formatBytes(c.bytes)} · {c.models.toLocaleString()} models
                    </span>
                  </summary>
                  <ul className="releases">
                    {c.releases.map((rl) => (
                      <li key={rl.name}>
                        <span className="name">{rl.name || "(no release)"}</span>
                        <span className="muted">
                          {formatBytes(rl.bytes)} · {rl.models} model{rl.models === 1 ? "" : "s"}
                        </span>
                      </li>
                    ))}
                  </ul>
                </details>
              </li>
            ))}
          </ul>
        </div>
        <div>
          <h3>By kind of file</h3>
          <ul className="job-items">
            {r.kinds.map((k) => (
              <li key={k.ext}>
                <span className="name">.{k.ext || "(none)"}</span>
                <span className="muted">
                  {formatBytes(k.bytes)} · {k.files.toLocaleString()} files
                </span>
              </li>
            ))}
          </ul>
          <h3>Biggest models</h3>
          <ul className="job-items">
            {r.largest.map((m) => (
              <li key={m.id}>
                <a href={`#/model/${m.id}`}>{m.name}</a>
                <span className="muted">
                  {m.creator} · {formatBytes(m.bytes)}
                </span>
              </li>
            ))}
          </ul>
        </div>
      </div>
    </section>
  );
}
