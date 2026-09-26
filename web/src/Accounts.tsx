import { useEffect, useState, type FormEvent } from "react";
import { auth, message, type Account, type Me } from "./auth";
import { formatDate } from "./api";

// Accounts: the roster, for admins.
export function Accounts({ me }: { me: Me }) {
  const [list, setList] = useState<Account[] | null>(null);
  const [error, setError] = useState("");
  const [msg, setMsg] = useState("");
  const [registration, setRegistration] = useState(true);
  const load = () => auth.accounts().then(setList, (e) => setError(message(e)));
  useEffect(() => {
    load();
    auth.options().then((o) => setRegistration(o.registration !== false), () => {});
  }, []);
  const change = (a: Account, ch: Parameters<typeof auth.changeAccount>[1], done: string) => {
    setError("");
    setMsg("");
    auth.changeAccount(a.id, ch).then(() => (setMsg(done), load()), (e) => setError(message(e)));
  };
  if (!list) return error ? <p className="error">{error}</p> : <p>Loading…</p>;
  return (
    <div className="settings">
      <h2>Accounts</h2>
      <p className="sub">
        {registration
          ? "Anyone who can reach the app can create an account (set OPEN_REGISTRATION=false to leave that to admins)."
          : "Only admins create accounts (OPEN_REGISTRATION=false)."}{" "}
        All accounts share the library, the print queue and the printer. Admins also manage settings, imports and
        accounts.
      </p>
      {error && <p className="error">{error}</p>}
      {msg && <p className="notice">{msg}</p>}
      <AddAccount onAdded={(a) => (setError(""), setMsg(`${a.email} added.`), load())} />
      <table className="roster">
        <thead>
          <tr>
            <th>Email</th>
            <th>Role</th>
            <th>Second factor</th>
            <th>Created</th>
            <th></th>
          </tr>
        </thead>
        <tbody>
          {list.map((a) => (
            <tr key={a.id} className={a.enabled ? "" : "disabled"}>
              <td>
                {a.email}
                {a.id === me.id && <span className="sub"> (you)</span>}
                {a.provider && <span className="sub"> · {a.provider}</span>}
                {a.waiting && <span className="sub"> · hasn't signed in yet</span>}
                {!a.enabled && <span className="sub"> · disabled</span>}
                {a.locked && <span className="sub"> · locked for a few minutes</span>}
              </td>
              <td>
                <select
                  value={a.role}
                  onChange={(e) => change(a, { role: e.target.value as Account["role"] }, `${a.email} is now ${e.target.value.toLowerCase()}.`)}
                >
                  <option value="USER">User</option>
                  <option value="ADMIN">Admin</option>
                </select>
              </td>
              <td>{a.mfa === "totp" ? "Authenticator app" : a.mfa === "webauthn" ? "Passkey" : "Not set up"}</td>
              <td>{formatDate(a.created)}</td>
              <td className="row-actions">
                <button
                  className="link"
                  onClick={() => change(a, { enabled: !a.enabled }, `${a.email} ${a.enabled ? "disabled" : "enabled"}.`)}
                >
                  {a.enabled ? "Disable" : "Enable"}
                </button>
                {a.mfa && (
                  <button
                    className="link"
                    onClick={() =>
                      confirm(`Reset the second factor of ${a.email}? They set up a new one at their next sign-in.`) &&
                      change(a, { resetMfa: true }, `Second factor of ${a.email} reset.`)
                    }
                  >
                    Reset second factor
                  </button>
                )}
                {!a.provider && (
                  <button
                    className="link"
                    onClick={() => {
                      const pw = prompt(`New password for ${a.email} (at least 12 characters):`);
                      if (pw) change(a, { newPassword: pw }, `Password of ${a.email} changed.`);
                    }}
                  >
                    Set password
                  </button>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function AddAccount({ onAdded }: { onAdded: (a: Account) => void }) {
  const [open, setOpen] = useState(false);
  const [google, setGoogle] = useState(false);
  const [googleAvailable, setGoogleAvailable] = useState(false);
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [role, setRole] = useState<Account["role"]>("USER");
  const [error, setError] = useState("");
  useEffect(() => {
    auth.options().then((o) => setGoogleAvailable(!!o.google), () => {});
  }, []);
  if (!open) return <button onClick={() => setOpen(true)}>Add an account</button>;
  const submit = (e: FormEvent) => {
    e.preventDefault();
    setError("");
    auth.createAccount({ email, role, ...(google ? { google: true } : { password }) }).then(
      (a) => (setOpen(false), setEmail(""), setPassword(""), onAdded(a)),
      (e) => setError(message(e)),
    );
  };
  return (
    <form className="add-account" onSubmit={submit}>
      <label>
        Email
        <input type="email" value={email} onChange={(e) => setEmail(e.target.value)} required autoFocus />
      </label>
      {googleAvailable && (
        <label>
          Signs in with
          <select value={google ? "google" : "password"} onChange={(e) => setGoogle(e.target.value === "google")}>
            <option value="password">a password</option>
            <option value="google">Google</option>
          </select>
        </label>
      )}
      {!google && (
        <label>
          <span>
            Initial password <span className="sub">(at least 12 characters; tell them to change it)</span>
          </span>
          <input type="text" autoComplete="off" minLength={12} value={password} onChange={(e) => setPassword(e.target.value)} required />
        </label>
      )}
      <label>
        Role
        <select value={role} onChange={(e) => setRole(e.target.value as Account["role"])}>
          <option value="USER">User</option>
          <option value="ADMIN">Admin</option>
        </select>
      </label>
      <p className="sub">They set up their second factor at the first sign-in.</p>
      {error && <p className="error">{error}</p>}
      <div className="row">
        <button className="primary">Add</button>
        <button type="button" onClick={() => setOpen(false)}>
          Cancel
        </button>
      </div>
    </form>
  );
}
