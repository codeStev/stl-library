// Sign-in API: the flow of the campaign organizer. A correct password only
// earns a short-lived pending token (kept in memory, sent as Bearer); the
// second factor turns it into the session, which the server keeps in an
// HttpOnly cookie.

export interface Me {
  id: string;
  email: string;
  role: "ADMIN" | "USER";
  mfa: "" | "totp" | "webauthn";
  external: boolean;
  passkeys: boolean; // passkeys can be used on this server
  recoveryCodes?: string[]; // right after enrollment, shown once
}

export interface LoginResult {
  status: "MFA_SETUP_REQUIRED" | "MFA_CHALLENGE_REQUIRED";
  pendingToken: string;
  method?: "totp" | "webauthn";
  passkeys: boolean;
}

export interface AuthOptions {
  google?: boolean;
  passkeys?: boolean;
  open?: boolean; // the server runs without sign-in
}

export interface DeviceSession {
  id: string;
  created: number;
  expires: number;
  userAgent: string;
  ip: string;
  current: boolean;
}

export interface Passkey {
  id: string;
  name: string;
  created: number;
}

export interface Account {
  id: string;
  email: string;
  role: "ADMIN" | "USER";
  enabled: boolean;
  mfa: string;
  provider?: string;
  locked: boolean;
  created: number;
}

// HttpError keeps the status so pages can tell "wrong code" from "too
// many attempts".
export class HttpError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message);
  }
}

async function call<T>(method: string, path: string, body?: unknown, token?: string): Promise<T> {
  const headers: Record<string, string> = {};
  if (body !== undefined) headers["Content-Type"] = "application/json";
  if (token) headers["Authorization"] = `Bearer ${token}`;
  const r = await fetch(path, {
    method,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
    signal: AbortSignal.timeout(20_000),
  });
  if (!r.ok) throw new HttpError(r.status, (await r.text()).trim());
  if (r.status === 202 || r.status === 204) return undefined as T;
  return r.json() as Promise<T>;
}

// A readable message for a failed call.
export function message(e: unknown, unauthorized = "Invalid credentials."): string {
  if (e instanceof HttpError) {
    if (e.status === 401) return unauthorized;
    if (e.status === 429) return "Too many attempts - wait a minute and try again.";
    return e.message || `Error ${e.status}`;
  }
  if (e instanceof DOMException && e.name === "TimeoutError") return "No answer from the server.";
  return e instanceof Error ? e.message : String(e);
}

export const auth = {
  options: () => call<AuthOptions>("GET", "/api/auth/options"),
  me: () => call<Me>("GET", "/api/accounts/me"),
  register: (email: string, password: string) => call<void>("POST", "/api/accounts/register", { email, password }),
  login: (email: string, password: string) => call<LoginResult>("POST", "/api/auth/login", { email, password }),
  recoverPassword: (email: string, recoveryCode: string, newPassword: string) =>
    call<void>("POST", "/api/auth/recover-password", { email, recoveryCode, newPassword }),
  redeemGoogle: (code: string) => call<LoginResult>("POST", "/api/auth/oidc/exchange", { code }),

  startTotp: (token?: string) =>
    call<{ secret: string; otpauthUrl: string; qr: string }>("POST", "/api/auth/mfa/totp/setup", undefined, token),
  confirmTotp: (code: string, token?: string) => call<Me>("POST", "/api/auth/mfa/totp/confirm", { code }, token),
  verifyTotp: (code: string, token: string) => call<Me>("POST", "/api/auth/mfa/verify", { code }, token),
  verifyRecoveryCode: (code: string, token: string) =>
    call<LoginResult>("POST", "/api/auth/mfa/verify-recovery-code", { code }, token),

  logout: () => call<void>("POST", "/api/auth/logout"),
  logoutAll: () => call<void>("POST", "/api/auth/logout-all"),
  changePassword: (currentPassword: string, newPassword: string) =>
    call<Me>("POST", "/api/accounts/me/password", { currentPassword, newPassword }),
  sessions: () => call<DeviceSession[]>("GET", "/api/accounts/me/sessions"),
  revokeSession: (id: string) => call<void>("DELETE", `/api/accounts/me/sessions/${encodeURIComponent(id)}`),
  regenerateRecoveryCodes: () => call<{ recoveryCodes: string[] }>("POST", "/api/accounts/me/recovery-codes"),
  passkeys: () => call<Passkey[]>("GET", "/api/accounts/me/passkeys"),
  deletePasskey: (id: string) => call<void>("DELETE", `/api/accounts/me/passkeys/${id}`),

  accounts: () => call<Account[]>("GET", "/api/accounts"),
  changeAccount: (
    id: string,
    change: { role?: "ADMIN" | "USER"; enabled?: boolean; newPassword?: string; resetMfa?: boolean },
  ) => call<void>("PATCH", `/api/accounts/${id}`, change),
};

// ---- passkeys (WebAuthn) ----

const b64urlToBuf = (s: string): ArrayBuffer => {
  const b = atob(s.replace(/-/g, "+").replace(/_/g, "/") + "===".slice((s.length + 3) % 4));
  const out = new Uint8Array(b.length);
  for (let i = 0; i < b.length; i++) out[i] = b.charCodeAt(i);
  return out.buffer;
};

const bufToB64url = (buf: ArrayBuffer): string => {
  let s = "";
  for (const c of new Uint8Array(buf)) s += String.fromCharCode(c);
  return btoa(s).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
};

type Descriptor = { id: string; type: PublicKeyCredentialType; transports?: AuthenticatorTransport[] };
const descriptors = (list?: Descriptor[]) => list?.map((c) => ({ ...c, id: b64urlToBuf(c.id) }));

// Creates a passkey; with a pending token it becomes the second factor
// (the answer is the signed-in account), otherwise it is added to the
// signed-in account (no answer).
export async function registerPasskey(name: string, token?: string): Promise<Me | undefined> {
  const { publicKey: o } = await call<{ publicKey: any }>(
    "POST",
    "/api/auth/mfa/webauthn/register/begin",
    undefined,
    token,
  );
  const cred = (await navigator.credentials.create({
    publicKey: {
      ...o,
      challenge: b64urlToBuf(o.challenge),
      user: { ...o.user, id: b64urlToBuf(o.user.id) },
      excludeCredentials: descriptors(o.excludeCredentials),
    },
  })) as PublicKeyCredential | null;
  if (!cred) throw new Error("No passkey was created.");
  const r = cred.response as AuthenticatorAttestationResponse;
  const response = {
    id: cred.id,
    rawId: bufToB64url(cred.rawId),
    type: cred.type,
    authenticatorAttachment: cred.authenticatorAttachment ?? undefined,
    clientExtensionResults: cred.getClientExtensionResults(),
    response: {
      attestationObject: bufToB64url(r.attestationObject),
      clientDataJSON: bufToB64url(r.clientDataJSON),
      transports: r.getTransports?.() ?? [],
    },
  };
  return call<Me | undefined>("POST", "/api/auth/mfa/webauthn/register/finish", { name, response }, token);
}

// Proves the second factor with a passkey.
export async function passkeyLogin(token: string): Promise<Me> {
  const { publicKey: o } = await call<{ publicKey: any }>("POST", "/api/auth/mfa/webauthn/login/begin", undefined, token);
  const cred = (await navigator.credentials.get({
    publicKey: { ...o, challenge: b64urlToBuf(o.challenge), allowCredentials: descriptors(o.allowCredentials) },
  })) as PublicKeyCredential | null;
  if (!cred) throw new Error("No passkey was used.");
  const r = cred.response as AuthenticatorAssertionResponse;
  const response = {
    id: cred.id,
    rawId: bufToB64url(cred.rawId),
    type: cred.type,
    clientExtensionResults: cred.getClientExtensionResults(),
    response: {
      authenticatorData: bufToB64url(r.authenticatorData),
      clientDataJSON: bufToB64url(r.clientDataJSON),
      signature: bufToB64url(r.signature),
      userHandle: r.userHandle ? bufToB64url(r.userHandle) : undefined,
    },
  };
  return call<Me>("POST", "/api/auth/mfa/webauthn/login/finish", { response }, token);
}

// A guess at a passkey name from the browser ("Firefox on Linux").
export function deviceName(): string {
  const ua = navigator.userAgent;
  const browser = /Firefox\//.test(ua) ? "Firefox" : /Edg\//.test(ua) ? "Edge" : /Chrome\//.test(ua) ? "Chrome" : /Safari\//.test(ua) ? "Safari" : "Browser";
  const os = /Android/.test(ua) ? "Android" : /iPhone|iPad/.test(ua) ? "iOS" : /Mac OS/.test(ua) ? "macOS" : /Windows/.test(ua) ? "Windows" : /Linux/.test(ua) ? "Linux" : "";
  return os ? `${browser} on ${os}` : browser;
}

// Every API call that gets a 401 tells the app it was signed out.
export const SIGNED_OUT = "stlib:signed-out";
export const signedOut = () => dispatchEvent(new Event(SIGNED_OUT));
