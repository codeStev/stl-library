import { useEffect, useState, type FormEvent, type ReactNode } from "react";
import {
  auth,
  deviceName,
  HttpError,
  message,
  passkeyLogin,
  registerPasskey,
  SIGNED_OUT,
  type AuthOptions,
  type LoginResult,
  type Me,
} from "./auth";

type Stage =
  | { kind: "loading" }
  | { kind: "login"; notice?: string; error?: string }
  | { kind: "register" }
  | { kind: "recover" }
  | { kind: "setup"; pending: LoginResult }
  | { kind: "challenge"; pending: LoginResult }
  | { kind: "codes"; me: Me }
  | { kind: "in"; me: Me };

// SignedIn shows the sign-in pages until there is a full session, then
// renders children with the account.
export function SignedIn({ children }: { children: (me: Me, signOut: () => void) => ReactNode }) {
  const [stage, setStage] = useState<Stage>({ kind: "loading" });
  const [options, setOptions] = useState<AuthOptions>({});

  useEffect(() => {
    auth.options().then(setOptions, () => {});
    const q = new URLSearchParams(location.search);
    const code = q.get("code"), authError = q.get("authError");
    if (code || authError) history.replaceState(null, "", location.pathname + location.hash);
    if (authError) {
      setStage({ kind: "login", error: authError });
    } else if (code) {
      auth.redeemGoogle(code).then(
        (r) => setStage(pendingStage(r)),
        () => setStage({ kind: "login", error: "Google sign-in expired - try again." }),
      );
    } else {
      auth.me().then(
        (me) => setStage({ kind: "in", me }),
        () => setStage({ kind: "login" }),
      );
    }
    const out = () => setStage((s) => (s.kind === "in" ? { kind: "login", notice: "You were signed out." } : s));
    addEventListener(SIGNED_OUT, out);
    return () => removeEventListener(SIGNED_OUT, out);
  }, []);

  // Registration may have closed since (e.g. after the first account).
  useEffect(() => {
    if (stage.kind === "login") auth.options().then(setOptions, () => {});
  }, [stage.kind]);

  const signOut = () => {
    auth.logout().finally(() => setStage({ kind: "login", notice: "Signed out." }));
  };
  const signedIn = (me: Me) => setStage(me.recoveryCodes?.length ? { kind: "codes", me } : { kind: "in", me });

  switch (stage.kind) {
    case "loading":
      return <p className="auth-loading">Loading…</p>;
    case "in":
      return <>{children(stage.me, signOut)}</>;
    case "login":
      return (
        <Login
          options={options}
          notice={stage.notice}
          error={stage.error}
          onPending={(r) => setStage(pendingStage(r))}
          go={(kind) => setStage({ kind } as Stage)}
        />
      );
    case "register":
      return (
        <Register
          back={(notice) => setStage({ kind: "login", notice })}
        />
      );
    case "recover":
      return <Recover back={(notice) => setStage({ kind: "login", notice })} />;
    case "setup":
      return <MfaSetup pending={stage.pending} onDone={signedIn} cancel={() => setStage({ kind: "login" })} />;
    case "challenge":
      return (
        <MfaChallenge
          pending={stage.pending}
          onDone={signedIn}
          onReset={(r) => setStage({ kind: "setup", pending: r })}
          cancel={() => setStage({ kind: "login" })}
        />
      );
    case "codes":
      return (
        <Card title="Save your recovery codes">
          <RecoveryCodes codes={stage.me.recoveryCodes!} />
          <button className="primary" onClick={() => setStage({ kind: "in", me: stage.me })}>
            I saved them - continue
          </button>
        </Card>
      );
  }
}

function pendingStage(r: LoginResult): Stage {
  return r.status === "MFA_SETUP_REQUIRED" ? { kind: "setup", pending: r } : { kind: "challenge", pending: r };
}

function Card({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div className="auth">
      <div className="auth-card">
        <div className="brand">STL Library</div>
        <h1>{title}</h1>
        {children}
      </div>
    </div>
  );
}

function Login(props: {
  options: AuthOptions;
  notice?: string;
  error?: string;
  onPending: (r: LoginResult) => void;
  go: (kind: "register" | "recover") => void;
}) {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState(props.error ?? "");
  const [busy, setBusy] = useState(false);
  const submit = (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError("");
    auth
      .login(email, password)
      .then(props.onPending, (e) => setError(message(e, "Invalid email or password.")))
      .finally(() => setBusy(false));
  };
  return (
    <Card title="Sign in">
      {props.notice && <p className="notice">{props.notice}</p>}
      <form onSubmit={submit}>
        <label>
          Email
          <input type="email" autoComplete="username" value={email} onChange={(e) => setEmail(e.target.value)} required autoFocus />
        </label>
        <label>
          Password
          <input type="password" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} required />
        </label>
        {error && <p className="error">{error}</p>}
        <button className="primary" disabled={busy}>
          {busy ? "Signing in…" : "Sign in"}
        </button>
      </form>
      {props.options.google && (
        <a className="button google" href="/oauth2/authorization/google">
          Sign in with Google
        </a>
      )}
      <p className="links">
        {props.options.registration !== false && (
          <button className="link" onClick={() => props.go("register")}>
            Create an account
          </button>
        )}
        <button className="link" onClick={() => props.go("recover")}>
          Forgot password?
        </button>
      </p>
    </Card>
  );
}

function Register({ back }: { back: (notice?: string) => void }) {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [repeat, setRepeat] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (password !== repeat) return setError("The passwords don't match.");
    setBusy(true);
    setError("");
    auth
      .register(email, password)
      // The same answer whether or not the address already had an account.
      .then(() => back("Account created - sign in to set up your second factor."), (e) => setError(message(e)))
      .finally(() => setBusy(false));
  };
  return (
    <Card title="Create an account">
      <form onSubmit={submit}>
        <label>
          Email
          <input type="email" autoComplete="username" value={email} onChange={(e) => setEmail(e.target.value)} required autoFocus />
        </label>
        <label>
          <span>Password <span className="sub">(at least 12 characters)</span></span>
          <input type="password" autoComplete="new-password" minLength={12} value={password} onChange={(e) => setPassword(e.target.value)} required />
        </label>
        <label>
          Repeat password
          <input type="password" autoComplete="new-password" value={repeat} onChange={(e) => setRepeat(e.target.value)} required />
        </label>
        {error && <p className="error">{error}</p>}
        <button className="primary" disabled={busy}>
          {busy ? "Creating…" : "Create account"}
        </button>
      </form>
      <p className="links">
        <button className="link" onClick={() => back()}>
          Back to sign in
        </button>
      </p>
    </Card>
  );
}

function Recover({ back }: { back: (notice?: string) => void }) {
  const [email, setEmail] = useState("");
  const [code, setCode] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const submit = (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError("");
    auth
      .recoverPassword(email, code, password)
      .then(
        () => back("Password changed - sign in with the new one."),
        (e) => setError(message(e, "That email and recovery code don't match.")),
      )
      .finally(() => setBusy(false));
  };
  return (
    <Card title="Reset your password">
      <p className="sub">Use one of the recovery codes you saved when you set up your account. Each code works once.</p>
      <form onSubmit={submit}>
        <label>
          Email
          <input type="email" autoComplete="username" value={email} onChange={(e) => setEmail(e.target.value)} required autoFocus />
        </label>
        <label>
          Recovery code
          <input value={code} onChange={(e) => setCode(e.target.value)} placeholder="xxxxx-xxxxx" autoComplete="off" required />
        </label>
        <label>
          <span>New password <span className="sub">(at least 12 characters)</span></span>
          <input type="password" autoComplete="new-password" minLength={12} value={password} onChange={(e) => setPassword(e.target.value)} required />
        </label>
        {error && <p className="error">{error}</p>}
        <button className="primary" disabled={busy}>
          {busy ? "Saving…" : "Set new password"}
        </button>
      </form>
      <p className="links">
        <button className="link" onClick={() => back()}>
          Back to sign in
        </button>
      </p>
    </Card>
  );
}

// TotpSetup: QR code, then the first code. Used at enrollment (with the
// pending token) and to switch to a new authenticator (signed in).
export function TotpSetup({ token, onDone }: { token?: string; onDone: (me: Me) => void }) {
  const [setup, setSetup] = useState<{ secret: string; qr: string } | null>(null);
  const [code, setCode] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    auth.startTotp(token).then(setSetup, (e) => setError(message(e)));
  }, [token]);
  if (!setup) return error ? <p className="error">{error}</p> : <p>Preparing…</p>;
  const submit = (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError("");
    auth
      .confirmTotp(code.trim(), token)
      .then(onDone, (e) => setError(message(e, "That code is not right - check the app and try again.")))
      .finally(() => setBusy(false));
  };
  return (
    <form onSubmit={submit} className="totp-setup">
      <p className="sub">Scan this with an authenticator app (Aegis, 2FAS, Google Authenticator, 1Password …).</p>
      <img src={setup.qr} alt="QR code for the authenticator app" width={200} height={200} />
      <p className="sub">
        Or enter the key by hand: <code>{setup.secret}</code>
      </p>
      <label>
        6-digit code from the app
        <input value={code} onChange={(e) => setCode(e.target.value)} inputMode="numeric" autoComplete="one-time-code" maxLength={6} required autoFocus />
      </label>
      {error && <p className="error">{error}</p>}
      <button className="primary" disabled={busy || code.trim().length < 6}>
        {busy ? "Checking…" : "Confirm"}
      </button>
    </form>
  );
}

function MfaSetup({ pending, onDone, cancel }: { pending: LoginResult; onDone: (me: Me) => void; cancel: () => void }) {
  const [method, setMethod] = useState<"" | "totp">("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const passkey = () => {
    setBusy(true);
    setError("");
    registerPasskey(deviceName(), pending.pendingToken)
      .then((me) => me && onDone(me), (e) => setError(passkeyMessage(e)))
      .finally(() => setBusy(false));
  };
  return (
    <Card title="Set up your second factor">
      {method === "totp" ? (
        <TotpSetup token={pending.pendingToken} onDone={onDone} />
      ) : (
        <>
          <p className="sub">Every account needs a second factor. Choose how you want to prove it's you.</p>
          <button className="primary" onClick={() => setMethod("totp")}>
            Use an authenticator app
          </button>
          {pending.passkeys && (
            <button className="primary" onClick={passkey} disabled={busy}>
              {busy ? "Follow your browser's prompt…" : "Use a passkey or security key"}
            </button>
          )}
          {error && <p className="error">{error}</p>}
        </>
      )}
      <p className="links">
        <button className="link" onClick={cancel}>
          Cancel
        </button>
      </p>
    </Card>
  );
}

function passkeyMessage(e: unknown): string {
  if (e instanceof DOMException && (e.name === "NotAllowedError" || e.name === "AbortError"))
    return "The passkey prompt was cancelled or timed out. Try again.";
  if (e instanceof DOMException && e.name === "InvalidStateError") return "This authenticator already holds a passkey for this account.";
  return message(e, "The passkey was not accepted.");
}

function MfaChallenge(props: {
  pending: LoginResult;
  onDone: (me: Me) => void;
  onReset: (r: LoginResult) => void;
  cancel: () => void;
}) {
  const [recovery, setRecovery] = useState(false);
  const [code, setCode] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const token = props.pending.pendingToken;
  const passkey = () => {
    setBusy(true);
    setError("");
    passkeyLogin(token)
      .then(props.onDone, (e) => setError(passkeyMessage(e)))
      .finally(() => setBusy(false));
  };
  const submit = (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError("");
    const done = recovery
      ? auth.verifyRecoveryCode(code, token).then(props.onReset)
      : auth.verifyTotp(code.trim(), token).then(props.onDone);
    done
      .catch((e) => {
        if (e instanceof HttpError && e.status === 401 && !recovery) setError("That code is not right.");
        else setError(message(e, "That recovery code is not valid."));
      })
      .finally(() => setBusy(false));
  };
  return (
    <Card title="Two-factor check">
      {recovery ? (
        <form onSubmit={submit}>
          <p className="sub">
            Lost your authenticator? A recovery code signs you in once and lets you set up a new second factor.
          </p>
          <label>
            Recovery code
            <input value={code} onChange={(e) => setCode(e.target.value)} placeholder="xxxxx-xxxxx" autoComplete="off" required autoFocus />
          </label>
          {error && <p className="error">{error}</p>}
          <button className="primary" disabled={busy}>
            Use recovery code
          </button>
        </form>
      ) : props.pending.method === "webauthn" ? (
        <>
          <p className="sub">Use your passkey to finish signing in.</p>
          {error && <p className="error">{error}</p>}
          <button className="primary" onClick={passkey} disabled={busy}>
            {busy ? "Follow your browser's prompt…" : "Use passkey"}
          </button>
        </>
      ) : (
        <form onSubmit={submit}>
          <label>
            6-digit code from your authenticator app
            <input value={code} onChange={(e) => setCode(e.target.value)} inputMode="numeric" autoComplete="one-time-code" maxLength={6} required autoFocus />
          </label>
          {error && <p className="error">{error}</p>}
          <button className="primary" disabled={busy || code.trim().length < 6}>
            {busy ? "Checking…" : "Verify"}
          </button>
        </form>
      )}
      <p className="links">
        <button
          className="link"
          onClick={() => {
            setRecovery(!recovery);
            setCode("");
            setError("");
          }}
        >
          {recovery ? "Back" : "Use a recovery code"}
        </button>
        <button className="link" onClick={props.cancel}>
          Cancel
        </button>
      </p>
    </Card>
  );
}

export function RecoveryCodes({ codes }: { codes: string[] }) {
  const text = codes.join("\n");
  return (
    <div className="recovery-codes">
      <p className="sub">
        Each code works once - to sign in without your second factor, or to reset a forgotten password. Keep them
        somewhere safe; they are not shown again.
      </p>
      <ol>
        {codes.map((c) => (
          <li key={c}>
            <code>{c}</code>
          </li>
        ))}
      </ol>
      <div className="row">
        <button onClick={() => navigator.clipboard?.writeText(text)}>Copy</button>
        <a
          className="button"
          download="stl-library-recovery-codes.txt"
          href={`data:text/plain;charset=utf-8,${encodeURIComponent(text + "\n")}`}
        >
          Download
        </a>
      </div>
    </div>
  );
}
