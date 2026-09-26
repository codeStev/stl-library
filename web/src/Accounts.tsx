import { useEffect, useState } from "react";
import { auth, message, type Account, type Me } from "./auth";
import { formatDate } from "./api";

// Accounts: the roster, for admins.
export function Accounts({ me }: { me: Me }) {
  const [list, setList] = useState<Account[] | null>(null);
  const [error, setError] = useState("");
  const [msg, setMsg] = useState("");
  const load = () => auth.accounts().then(setList, (e) => setError(message(e)));
  useEffect(() => {
    load();
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
        Anyone can create an account; the first one became the admin. All accounts share the library, the print queue
        and the printer. Admins also manage settings, imports and accounts.
      </p>
      {error && <p className="error">{error}</p>}
      {msg && <p className="notice">{msg}</p>}
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
              <td className="actions">
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
