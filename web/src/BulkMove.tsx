import { useEffect, useState } from "react";
import { api, type BulkBody, type BulkRow, type Creator } from "./api";

// Move or rename the selected models: another creator, release or category, and/or a find-and-replace in the folder
// names. Shows what would happen first; the folders are only moved after a confirmation, and each move can be undone
// on the "Not following the convention" page.
export function BulkMove({ ids, onDone, onClose }: { ids: number[]; onDone: (message: string) => void; onClose: () => void }) {
  const [creator, setCreator] = useState("");
  const [release, setRelease] = useState("");
  const [category, setCategory] = useState("");
  const [dropRelease, setDropRelease] = useState(false);
  const [dropCategory, setDropCategory] = useState(false);
  const [find, setFind] = useState("");
  const [replace, setReplace] = useState("");
  const [creators, setCreators] = useState<Creator[]>([]);
  const [rows, setRows] = useState<BulkRow[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  useEffect(() => {
    api.creators().then(setCreators, () => {});
  }, []);

  const body = (): BulkBody => ({
    ids,
    creator: creator.trim() || null,
    release: dropRelease ? "" : release.trim() || null,
    category: dropCategory ? "" : category.trim() || null,
    find,
    replace,
  });
  const nothing = !creator.trim() && !release.trim() && !category.trim() && !dropRelease && !dropCategory && !find;
  const change = (set: (v: string) => void) => (e: { target: { value: string } }) => {
    set(e.target.value);
    setRows(null);
  };
  const preview = () => {
    setBusy(true);
    api.bulkPlan(body()).then(
      (r) => (setRows(r.rows), setError("")),
      (e) => setError(String(e)),
    ).finally(() => setBusy(false));
  };
  const movable = rows?.filter((r) => !r.problem) ?? [];
  const apply = () => {
    if (!confirm(`Move ${movable.length} folder${movable.length === 1 ? "" : "s"} in the library? Tags, prints and collections move along; each move can be undone later.`)) return;
    setBusy(true);
    api.bulkApply(body()).then(
      (r) => onDone(`Moved ${r.done} model${r.done === 1 ? "" : "s"}${r.failed.length ? `, ${r.failed.length} failed: ${r.failed.slice(0, 2).join("; ")}` : ""}.`),
      (e) => setError(String(e)),
    ).finally(() => setBusy(false));
  };

  return (
    <div className="bulkmove">
      <div className="head">
        <strong>
          Move or rename {ids.length} model{ids.length === 1 ? "" : "s"}
        </strong>
        <button className="link" onClick={onClose}>
          close
        </button>
      </div>
      <p className="muted">Leave a field empty to keep it as it is. The folders of the models are moved on disk.</p>
      <div className="fields">
        <label>
          Creator
          <input list="bulk-creators" value={creator} onChange={change(setCreator)} placeholder="(unchanged)" maxLength={120} />
          <datalist id="bulk-creators">
            {creators.map((c) => (
              <option key={c.name} value={c.name} />
            ))}
          </datalist>
        </label>
        <label>
          Release
          <input value={dropRelease ? "" : release} disabled={dropRelease} onChange={change(setRelease)} placeholder="(unchanged)" maxLength={120} />
          <span className="check">
            <input type="checkbox" checked={dropRelease} onChange={(e) => (setDropRelease(e.target.checked), setRows(null))} /> no release level
          </span>
        </label>
        <label>
          Category
          <input value={dropCategory ? "" : category} disabled={dropCategory} onChange={change(setCategory)} placeholder="(unchanged)" maxLength={120} />
          <span className="check">
            <input type="checkbox" checked={dropCategory} onChange={(e) => (setDropCategory(e.target.checked), setRows(null))} /> no category level
          </span>
        </label>
        <label>
          In the model names, replace
          <input value={find} onChange={change(setFind)} placeholder="text to find" maxLength={120} />
        </label>
        <label>
          with
          <input value={replace} onChange={change(setReplace)} placeholder="(nothing)" maxLength={120} />
        </label>
      </div>
      <div className="actions">
        <button disabled={busy || nothing} onClick={preview}>
          Show what would happen
        </button>
        {rows && movable.length > 0 && (
          <button disabled={busy} onClick={apply}>
            Move {movable.length} folder{movable.length === 1 ? "" : "s"}
          </button>
        )}
      </div>
      {error && <p className="error">{error}</p>}
      {rows && (
        <ul className="bulkrows">
          {rows.map((r) => (
            <li key={r.id} className={r.problem ? "problem" : ""}>
              <strong>{r.name}</strong>
              {r.problem ? (
                <span className="muted"> · {r.problem}</span>
              ) : (
                <div>
                  <code>{r.from}</code> → <code>{r.to}</code>
                </div>
              )}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
