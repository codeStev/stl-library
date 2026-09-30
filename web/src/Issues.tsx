import { useEffect, useMemo, useState } from "react";
import { api, formatDate, type FixesState, type Issue } from "./api";

// Folders that don't follow the convention, grouped by creator and reason.
// The app never changes them; this is the to-do list for fixing them.
export function Issues({ admin = false }: { admin?: boolean }) {
  const [fixes, setFixes] = useState<FixesState | null>(null);
  const [fixNote, setFixNote] = useState("");
  const loadFixes = () => api.fixes().then(setFixes, () => {});
  useEffect(() => {
    loadFixes();
  }, []);
  const runFix = (p: Promise<unknown>, done: string) =>
    p.then(
      () => {
        setFixNote(`${done} The library rescans now; the list updates in a moment.`);
        setTimeout(() => {
          loadFixes();
          api.issues().then(setIssues, () => {});
        }, 4000);
        return loadFixes();
      },
      (e) => setFixNote(String(e).replace(/^Error: /, "")),
    );
  const [issues, setIssues] = useState<Issue[] | null>(null);
  const [error, setError] = useState("");
  const [filter, setFilter] = useState("");

  useEffect(() => {
    api.issues().then(setIssues, (e) => setError(String(e)));
  }, []);

  const groups = useMemo(() => {
    const out = new Map<string, Issue[]>();
    for (const i of issues ?? []) {
      if (filter && !i.dir.toLowerCase().includes(filter.toLowerCase())) continue;
      const creator = i.dir.split("/")[0] || "(library root)";
      out.set(creator, [...(out.get(creator) ?? []), i]);
    }
    return [...out.entries()].sort((a, b) => b[1].length - a[1].length);
  }, [issues, filter]);

  if (error) return <p className="error">{error}</p>;
  if (!issues) return <p>Loading…</p>;
  return (
    <div className="issues">
      <p>
        {issues.length} folders don't follow the folder convention. Their files are not shown in the library until
        they are moved into place.
      </p>
      {fixes && fixes.suggestions.length > 0 && (
        <details className="fixes" open>
          <summary>
            Suggested fixes <span className="count">{fixes.suggestions.length}</span>
          </summary>
          <p className="muted">
            Renames of one folder to its canonical spelling, only where every word of the name is understood. Nothing happens until you apply one;
            each can be undone below.
            {!fixes.canApply && " (This server cannot change the library.)"}
          </p>
          <ul>
            {fixes.suggestions.slice(0, 100).map((s) => (
              <li key={s.from}>
                <code>{s.from}</code> → <code>{s.to.split("/").pop()}</code>
                <span className="reason">clears {s.issues} folder{s.issues === 1 ? "" : "s"}</span>
                {admin && fixes.canApply && (
                  <button
                    className="link"
                    onClick={() => {
                      if (confirm(`Rename\n${s.from}\nto\n${s.to.split("/").pop()}?`)) runFix(api.applyFix(s.from, s.to), "Renamed.");
                    }}
                  >
                    Apply
                  </button>
                )}
              </li>
            ))}
          </ul>
        </details>
      )}
      {fixNote && <p className="muted">{fixNote}</p>}
      {fixes && fixes.journal.length > 0 && (
        <details>
          <summary>
            Applied fixes <span className="count">{fixes.journal.length}</span>
          </summary>
          <ul>
            {fixes.journal.map((j) => (
              <li key={j.id} className={j.undone ? "undone" : ""}>
                <code>{j.from}</code> → <code>{j.to.split("/").pop()}</code>
                <span className="reason">{formatDate(j.atUnix)}{j.undone ? " · undone" : ""}</span>
                {admin && !j.undone && (
                  <button className="link" onClick={() => runFix(api.undoFix(j.id), "Undone.")}>
                    Undo
                  </button>
                )}
              </li>
            ))}
          </ul>
        </details>
      )}
      <div className="toolbar">
        <input type="search" placeholder="Filter folders…" value={filter} onChange={(e) => setFilter(e.target.value)} />
      </div>
      {groups.map(([creator, list]) => (
        <details key={creator}>
          <summary>
            {creator} <span className="count">{list.length}</span>
          </summary>
          <ul>
            {list.map((i) => (
              <li key={i.dir}>
                <code>{i.dir}</code>
                <span className="reason">{i.reason}</span>
              </li>
            ))}
          </ul>
        </details>
      ))}
    </div>
  );
}
