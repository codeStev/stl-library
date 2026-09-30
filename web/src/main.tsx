import { StrictMode, useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import { Library } from "./Library";
import { Collections } from "./Collections";
import { Prints } from "./Prints";
import { Health } from "./Health";
import { ModelPage } from "./ModelPage";
import { Issues } from "./Issues";
import { Queue } from "./Queue";
import { Imports } from "./Imports";
import { Printer } from "./Printer";
import { Settings } from "./Settings";
import { SignedIn } from "./SignIn";
import { AccountPage } from "./Account";
import { Accounts } from "./Accounts";
import type { Me } from "./auth";
import "./style.css";

// Hash routes: "#/" library, "#/model/12", "#/issues".
function useRoute(): string {
  const [hash, setHash] = useState(location.hash || "#/");
  useEffect(() => {
    const on = () => setHash(location.hash || "#/");
    addEventListener("hashchange", on);
    return () => removeEventListener("hashchange", on);
  }, []);
  return hash;
}

// The navigation, in the order shown. "match" names the routes that belong to an entry besides its own address.
const NAV: { to: string; label: string; title?: string; admin?: boolean; match?: RegExp }[] = [
  { to: "#/", label: "Library", match: /^#\/model\// },
  { to: "#/collections", label: "Collections" },
  { to: "#/prints", label: "Prints", match: /^#\/print\// },
  { to: "#/queue", label: "Print queue" },
  { to: "#/printer", label: "Printer" },
  { to: "#/imports", label: "Imports" },
  { to: "#/health", label: "Health", title: "Duplicates, damaged files, storage and leftovers" },
  { to: "#/issues", label: "Issues", title: "Folders that don't follow the folder convention, and suggested fixes" },
  { to: "#/settings", label: "Settings", admin: true },
  { to: "#/accounts", label: "Accounts", admin: true },
];

function isActive(route: string, to: string): boolean {
  if (to === "#/") return route === "#/" || route === "" || route.startsWith("#/model/");
  return route === to || route.startsWith(to + "/") || route.startsWith(to + "?");
}

// The title of the browser tab says where you are (history and tab lists show it).
function titleFor(route: string): string {
  const hit = NAV.find((n) => n.to !== "#/" && isActive(route, n.to));
  return hit ? `${hit.label} · STL Library` : "STL Library";
}

function App({ me, signOut }: { me: Me; signOut: () => void }) {
  const admin = me.role === "ADMIN";
  const route = useRoute();
  const model = route.match(/^#\/model\/(\d+)/);
  useEffect(() => {
    if (!model) document.title = titleFor(route); // a model page sets its own
    // on a phone the navigation scrolls: keep the current entry in view
    document.querySelector('header nav a[aria-current="page"]')?.scrollIntoView({ block: "nearest", inline: "center" });
  }, [route, model]);
  return (
    <>
      <header>
        <a href="#/" className="brand">
          STL Library
        </a>
        <nav>
          {NAV.filter((n) => !n.admin || admin).map((n) => (
            <a key={n.to} href={n.to} title={n.title} aria-current={isActive(route, n.to) ? "page" : undefined}>
              {n.label}
            </a>
          ))}
        </nav>
        {me.email && (
          <a href="#/account" className="me" title="Your account">
            {me.email}
          </a>
        )}
      </header>
      <main>
        {model ? (
          <ModelPage id={Number(model[1])} />
        ) : route.startsWith("#/issues") ? (
          <Issues admin={admin} />
        ) : route.startsWith("#/health") ? (
          <Health admin={admin} />
        ) : route.startsWith("#/prints") || route.startsWith("#/print/") ? (
          <Prints id={Number(route.match(/^#\/print\/(\d+)/)?.[1]) || null} />
        ) : route.startsWith("#/collections") ? (
          <Collections />
        ) : route.startsWith("#/queue") ? (
          <Queue />
        ) : route.startsWith("#/imports") ? (
          <Imports admin={admin} />
        ) : route.startsWith("#/printer") ? (
          <Printer />
        ) : route.startsWith("#/settings") && admin ? (
          <Settings />
        ) : route.startsWith("#/accounts") && admin ? (
          <Accounts me={me} />
        ) : route.startsWith("#/account") && me.id ? (
          <AccountPage me={me} signOut={signOut} />
        ) : (
          <Library admin={admin} />
        )}
      </main>
    </>
  );
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <SignedIn>{(me, signOut) => <App me={me} signOut={signOut} />}</SignedIn>
  </StrictMode>,
);
