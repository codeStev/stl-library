import { useEffect, useMemo, useState } from "react";
import { api, type Issue } from "./api";

// Folders that don't follow the convention, grouped by creator and reason.
// The app never changes them; this is the to-do list for fixing them.
export function Issues() {
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
