import { useEffect, useState, type FormEvent } from "react";
import { auth, deviceName, message, registerPasskey, type DeviceSession, type Me, type Passkey } from "./auth";
import { RecoveryCodes, TotpSetup } from "./SignIn";
import { formatDate } from "./api";

// Account: the caller's own security settings.
export function AccountPage({ me, signOut }: { me: Me; signOut: () => void }) {
  return (
    <div className="settings account">
      <h2>Your account</h2>
      <p className="sub">
        {me.email} · {me.role === "ADMIN" ? "Admin" : "User"}
        {me.external && " · signs in with Google"}
      </p>
      {!me.external && <PasswordSection />}
      <SecondFactorSection me={me} />
      <RecoverySection />
      <SessionsSection signOut={signOut} />
    </div>
  );
}

function PasswordSection() {
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [msg, setMsg] = useState("");
  const [error, setError] = useState("");
  const submit = (e: FormEvent) => {
    e.preventDefault();
    setMsg("");
    setError("");
    auth.changePassword(current, next).then(
      () => (setMsg("Password changed. Your other devices were signed out."), setCurrent(""), setNext("")),
      (e) => setError(message(e, "The current password is wrong.")),
    );
  };
  return (
    <section>
      <h3>Password</h3>
      <form onSubmit={submit}>
        <label>
          Current password
          <input type="password" autoComplete="current-password" value={current} onChange={(e) => setCurrent(e.target.value)} required />
        </label>
        <label>
          <span>New password <span className="sub">(at least 12 characters)</span></span>
          <input type="password" autoComplete="new-password" minLength={12} value={next} onChange={(e) => setNext(e.target.value)} required />
        </label>
        {error && <p className="error">{error}</p>}
        {msg && <p className="notice">{msg}</p>}
        <button className="primary">Change password</button>
      </form>
    </section>
  );
}

function SecondFactorSection({ me }: { me: Me }) {
  const [passkeys, setPasskeys] = useState<Passkey[]>([]);
  const [totp, setTotp] = useState(false);
  const [msg, setMsg] = useState("");
  const [error, setError] = useState("");
  const load = () => auth.passkeys().then(setPasskeys, (e) => setError(message(e)));
  useEffect(() => {
    load();
  }, []);
  const add = () => {
    setError("");
    setMsg("");
    registerPasskey(deviceName()).then(
      () => (setMsg("Passkey added."), load()),
      (e) => setError(e instanceof DOMException ? "The passkey prompt was cancelled." : message(e)),
    );
  };
  const remove = (p: Passkey) => {
    if (!confirm(`Remove the passkey "${p.name}"?`)) return;
    setError("");
    auth.deletePasskey(p.id).then(load, (e) => setError(message(e)));
  };
  return (
    <section>
      <h3>Second factor</h3>
      <p className="sub">
        {me.mfa === "totp" ? "You use an authenticator app." : me.mfa === "webauthn" ? "You use passkeys." : "Not set up."}
      </p>
      {me.mfa === "totp" &&
        (totp ? (
          <TotpSetup onDone={() => (setTotp(false), setMsg("New authenticator active. Your other devices were signed out."))} />
        ) : (
          <button onClick={() => setTotp(true)}>Move to a new authenticator app</button>
        ))}
      {me.passkeys && (
        <>
          <h4>Passkeys</h4>
          {passkeys.length === 0 ? (
            <p className="sub">No passkeys.</p>
          ) : (
            <ul className="plain">
              {passkeys.map((p) => (
                <li key={p.id}>
                  {p.name} <span className="sub">added {formatDate(p.created)}</span>
                  <button className="link" onClick={() => remove(p)}>
                    Remove
                  </button>
                </li>
              ))}
            </ul>
          )}
          <button onClick={add}>Add a passkey</button>
        </>
      )}
      {error && <p className="error">{error}</p>}
      {msg && <p className="notice">{msg}</p>}
    </section>
  );
}

function RecoverySection() {
  const [codes, setCodes] = useState<string[] | null>(null);
  const [error, setError] = useState("");
  const regenerate = () => {
    if (!confirm("Make new recovery codes? The old ones stop working.")) return;
    auth.regenerateRecoveryCodes().then((r) => setCodes(r.recoveryCodes), (e) => setError(message(e)));
  };
  return (
    <section>
      <h3>Recovery codes</h3>
      {codes ? <RecoveryCodes codes={codes} /> : <button onClick={regenerate}>Make new recovery codes</button>}
      {error && <p className="error">{error}</p>}
    </section>
  );
}

function SessionsSection({ signOut }: { signOut: () => void }) {
  const [list, setList] = useState<DeviceSession[]>([]);
  const [error, setError] = useState("");
  const load = () => auth.sessions().then(setList, (e) => setError(message(e)));
  useEffect(() => {
    load();
  }, []);
  return (
    <section>
      <h3>Signed-in devices</h3>
      <ul className="plain">
        {list.map((s) => (
          <li key={s.id}>
            <span>
              {s.userAgent ? shortAgent(s.userAgent) : "Unknown device"} <span className="sub">{s.ip} · since {formatDate(s.created)}</span>
              {s.current && <strong> (this device)</strong>}
            </span>
            {!s.current && (
              <button className="link" onClick={() => auth.revokeSession(s.id).then(load, (e) => setError(message(e)))}>
                Sign out
              </button>
            )}
          </li>
        ))}
      </ul>
      {error && <p className="error">{error}</p>}
      <div className="row">
        <button onClick={signOut}>Sign out</button>
        <button
          onClick={() => {
            if (confirm("Sign out on every device, including this one?")) auth.logoutAll().finally(signOut);
          }}
        >
          Sign out everywhere
        </button>
      </div>
    </section>
  );
}

function shortAgent(ua: string): string {
  const m = ua.match(/(Firefox|Edg|Chrome|Safari)\/[\d.]+/);
  const os = ua.match(/\(([^;)]+)/);
  return [m ? m[1].replace("Edg", "Edge") : ua.slice(0, 40), os ? os[1] : ""].filter(Boolean).join(" · ");
}
