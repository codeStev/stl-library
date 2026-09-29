import { useEffect, useState } from "react";
import { api, showLibrary, type Collection } from "./api";

// The collections page: make, rename and delete collections. A collection
// opens as the library filtered to it; models are added from the library
// ("Select models") or from a model's page.
export function Collections() {
  const [list, setList] = useState<Collection[]>([]);
  const [name, setName] = useState("");
  const [note, setNote] = useState("");
  const [editing, setEditing] = useState<number | null>(null);
  const [draft, setDraft] = useState({ name: "", note: "" });
  const [error, setError] = useState("");
  const load = () => api.collections().then(setList, (e) => setError(String(e)));
  useEffect(() => {
    load();
  }, []);
  const run = (p: Promise<unknown>) =>
    p.then(
      () => {
        setError("");
        return load();
      },
      (e) => setError(String(e).includes("409") ? "A collection with that name exists already." : String(e)),
    );
  const open = (c: Collection) => {
    showLibrary({ collection: String(c.id) });
    location.hash = "#/";
  };
  return (
    <div className="collections">
      <h1>Collections</h1>
      <form
        className="collection-new"
        onSubmit={(e) => {
          e.preventDefault();
          if (name.trim()) run(api.createCollection(name, note).then(() => (setName(""), setNote(""))));
        }}
      >
        <input value={name} onChange={(e) => setName(e.target.value)} placeholder="New collection, e.g. Dungeon night" maxLength={100} />
        <input value={note} onChange={(e) => setNote(e.target.value)} placeholder="note (optional)" maxLength={500} />
        <button disabled={!name.trim()}>Create</button>
      </form>
      {error && <p className="error">{error}</p>}
      {list.length === 0 && <p className="empty">No collections yet. Create one, then add models from the library.</p>}
      <ul className="collection-list">
        {list.map((c) => (
          <li key={c.id}>
            {editing === c.id ? (
              <form
                onSubmit={(e) => {
                  e.preventDefault();
                  run(api.updateCollection(c.id, draft.name, draft.note).then(() => setEditing(null)));
                }}
              >
                <input value={draft.name} onChange={(e) => setDraft({ ...draft, name: e.target.value })} maxLength={100} />
                <input value={draft.note} onChange={(e) => setDraft({ ...draft, note: e.target.value })} maxLength={500} placeholder="note" />
                <button>Save</button>
                <button type="button" className="link" onClick={() => setEditing(null)}>
                  Cancel
                </button>
              </form>
            ) : (
              <>
                <button className="link name" onClick={() => open(c)}>
                  {c.name}
                </button>
                <span className="muted">
                  {c.models} model{c.models === 1 ? "" : "s"}
                </span>
                {c.note && <span className="note">{c.note}</span>}
                <span className="spacer" />
                <button
                  className="link"
                  onClick={() => {
                    setEditing(c.id);
                    setDraft({ name: c.name, note: c.note });
                  }}
                >
                  Rename
                </button>
                <button
                  className="link"
                  onClick={() => {
                    if (confirm(`Delete the collection “${c.name}”? The models stay in the library.`)) run(api.deleteCollection(c.id));
                  }}
                >
                  Delete
                </button>
              </>
            )}
          </li>
        ))}
      </ul>
    </div>
  );
}
