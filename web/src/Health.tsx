import { useCallback, useEffect, useState } from "react";
import { Storage } from "./Storage";
import { api, formatBytes, formatDate, type DuplicateGroup, type SetGap, type HealthState, type TidyItem, type TidyState } from "./api";

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
        <h2>Backup</h2>
        {!h.backup.configured ? (
          <p className="muted">
            Not set up. Let your backup job touch a file when it is done (or point at the backup folder) and set <code>BACKUP_MARKER</code> to it; this page and the
            weekly summary then say when the last backup was and warn when it is overdue (<code>BACKUP_MAX_AGE</code>, default 168h).
          </p>
        ) : h.backup.error ? (
          <p className="error">{h.backup.error}</p>
        ) : (
          <p className={h.backup.overdue ? "error" : "muted"}>
            Last backup: {formatDate(h.backup.lastUnix)}
            {h.backup.overdue ? " - overdue!" : ""}
          </p>
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
                {e.otherCopies && e.otherCopies.length > 0 ? (
                  <span className="muted"> · the good content is also in: {e.otherCopies.join(", ")}</span>
                ) : (
                  <span className="error"> · no other copy in the library{h.backup.configured ? " - restore it from your backup" : ""}</span>
                )}
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

      <Storage />

      <Leftovers admin={admin} />

      <SetGaps />

      <Duplicates admin={admin} />
    </div>
  );
}

// Models whose supported and unsupported variants (otherwise the same) hold a different number of files.
function SetGaps() {
  const [gaps, setGaps] = useState<SetGap[] | null>(null);
  const [error, setError] = useState("");
  useEffect(() => {
    api.setGaps().then(setGaps, (e) => setError(String(e)));
  }, []);
  return (
    <section>
      <h2>Models with an incomplete set{gaps ? ` (${gaps.length})` : ""}</h2>
      <p className="muted">
        Variants that differ only in their supports should hold the same parts. Where one has fewer, a download probably lost files. The names are compared
        ignoring words like “supported”; a different naming can show up here too.
      </p>
      {error && <p className="error">{error}</p>}
      {gaps && gaps.length === 0 && <p className="muted">None found.</p>}
      {gaps?.map((g) => (
        <div key={g.modelId + g.sides.map((s) => s.label).join()} className="dup-group">
          <div className="dup-head">
            <a href={`#/model/${g.modelId}`}>{g.modelName}</a> · {g.sides.map((s) => `${s.label}: ${s.files}`).join(" · ")}
          </div>
          {g.missing.length > 0 && <div className="muted">No match in the others for: {g.missing.join(", ")}</div>}
        </div>
      ))}
    </section>
  );
}

function Duplicates({ admin }: { admin: boolean }) {
  const [min, setMin] = useState(64);
  const [groups, setGroups] = useState<DuplicateGroup[]>([]);
  const [total, setTotal] = useState(0);
  const [canMerge, setCanMerge] = useState(false);
  const [note, setNote] = useState("");
  const [error, setError] = useState("");
  const load = useCallback(
    (offset: number) =>
      api.duplicates(min, offset).then(
        (p) => {
          setTotal(p.total);
          setCanMerge(!!p.canMerge);
          setGroups((prev) => (offset === 0 ? p.groups : [...prev, ...p.groups]));
        },
        (e) => setError(String(e)),
      ),
    [min],
  );
  useEffect(() => {
    load(0);
  }, [load]);
  const merge = (g: DuplicateGroup, keepPart: number, remove: boolean) => {
    const keep = g.files.find((f) => f.partId === keepPart);
    const others = g.files.filter((f) => f.partId !== keepPart && (remove || !f.linked)).length;
    const text = remove
      ? `Keep ${keep?.path} and DELETE the other ${others} cop${others === 1 ? "y" : "ies"} from the disk? The models they belong to lose those files.`
      : `Keep ${keep?.path} and make the other ${others} cop${others === 1 ? "y" : "ies"} hard links to it? Every model keeps all its files; only the disk space is shared.`;
    if (!window.confirm(text)) return;
    setNote("");
    api.mergeDuplicates(g, keepPart, remove).then(
      (r) => {
        setNote(r.error ? `Done for ${r.merged}; ${r.error}` : `${remove ? "Deleted" : "Merged"} ${r.merged} file(s).`);
        load(0);
      },
      (e) => setError(String(e)),
    );
  };
  return (
    <section>
      <h2>Files that are in the library twice ({total})</h2>
      <p className="muted">
        Files with exactly the same content. {canMerge && admin ? "Pick the copy to keep: the others can be deleted from the disk, or turned into hard links to it (every model keeps all its files, the space is shared; works inside one disk). " : "Nothing is deleted here. "}{" "}
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
      {note && <p className="muted">{note}</p>}
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
                {f.linked && <span className="muted"> · hard link, no extra space</span>}
                {admin && canMerge && !f.linked && (
                  <button className="link" onClick={() => merge(g, f.partId, true)}>
                    keep this one, delete the others
                  </button>
                )}
                {admin && canMerge && !f.linked && (
                  <button className="link" onClick={() => merge(g, f.partId, false)}>
                    keep this one, link the others
                  </button>
                )}
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

const TIDY_TITLES: Record<TidyItem["kind"], string> = {
  "junk-file": "Junk files (.DS_Store, ._name, Thumbs.db) - deleted",
  "junk-folder": "Junk folders (__MACOSX) - deleted",
  "empty-folder": "Empty folders (or with only junk in them) - deleted",
  copy: "Copies with exactly the content of the original (“name (imported).stl”, “name (2).stl”) - moved to _duplicates",
};

// Leftovers of unpacking and importing. Finding them reads the whole folder tree, so it only happens on request.
function Leftovers({ admin }: { admin: boolean }) {
  const [t, setT] = useState<TidyState | null>(null);
  const [error, setError] = useState("");
  const [note, setNote] = useState("");
  const [off, setOff] = useState<Set<string>>(new Set()); // unchecked paths
  const load = useCallback(() => api.tidy().then(setT, (e) => setError(String(e))), []);
  useEffect(() => {
    load();
  }, [load]);
  const running = t?.running ?? false;
  useEffect(() => {
    if (!running) return;
    const i = setInterval(load, 2000);
    return () => clearInterval(i);
  }, [running, load]);
  if (!t || !t.enabled) return null;
  const chosen = t.items.filter((i) => !off.has(i.path));
  const count = (k: TidyItem["kind"]) => chosen.filter((i) => i.kind === k).length;
  const apply = () => {
    const deleted = count("junk-file") + count("junk-folder") + count("empty-folder");
    const msg =
      `Delete ${count("junk-file")} junk file(s), ${count("junk-folder")} junk folder(s) and ${count("empty-folder")} empty folder(s), ` +
      `and move ${count("copy")} copy file(s) to _duplicates?` +
      (deleted ? " Deleted junk cannot be brought back." : "");
    if (!confirm(msg)) return;
    api.tidyApply(chosen.map((i) => i.path)).then(
      (r) => {
        setNote(`${r.done} done${r.failed.length ? `, ${r.failed.length} failed: ${r.failed.slice(0, 3).join("; ")}` : ""}.`);
        setOff(new Set());
        load();
      },
      (e) => setError(String(e)),
    );
  };
  const kinds = (["junk-file", "junk-folder", "empty-folder", "copy"] as const).filter((k) => t.items.some((i) => i.kind === k));
  return (
    <section>
      <h2>Leftovers</h2>
      <p className="muted">
        Junk that archives and imports leave behind, folders with nothing in them, and copies that are byte for byte the same as the file next to them.
        Nothing happens until you confirm; model files are never deleted.
      </p>
      {error && <p className="error">{error}</p>}
      {note && <p className="muted">{note}</p>}
      <div className="actions">
        {admin && (
          <button disabled={running} onClick={() => api.tidyRun().then(() => setTimeout(load, 500), (e) => setError(String(e)))}>
            {t.finished ? "Search again" : "Search for leftovers"}
          </button>
        )}
        {running && <span className="progress">Looking… {t.folders.toLocaleString()} folders</span>}
        {!running && t.finished > 0 && <span className="muted">Searched {formatDate(t.finished)}: {t.items.length} found.</span>}
      </div>
      {t.error && <p className="error">{t.error}</p>}
      {kinds.map((k) => {
        const list = t.items.filter((i) => i.kind === k);
        return (
          <details key={k} open={list.length <= 40}>
            <summary>
              {TIDY_TITLES[k]} <span className="count">{list.length}</span>
            </summary>
            <ul className="job-items">
              {list.map((i) => (
                <li key={i.path}>
                  {admin && (
                    <input
                      type="checkbox"
                      checked={!off.has(i.path)}
                      onChange={() =>
                        setOff((o) => {
                          const n = new Set(o);
                          if (!n.delete(i.path)) n.add(i.path);
                          return n;
                        })
                      }
                      aria-label={`Include ${i.path}`}
                    />
                  )}
                  <span className="name">{i.path}</span>
                  {i.original && <span className="muted">same as {i.original.split("/").pop()}</span>}
                  {i.size ? <span className="muted">{formatBytes(i.size)}</span> : null}
                </li>
              ))}
            </ul>
          </details>
        );
      })}
      {admin && chosen.length > 0 && (
        <div className="actions">
          <button onClick={apply}>Clean up {chosen.length} selected</button>
        </div>
      )}
    </section>
  );
}
