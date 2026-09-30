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

function App({ me, signOut }: { me: Me; signOut: () => void }) {
  const admin = me.role === "ADMIN";
  const route = useRoute();
  const model = route.match(/^#\/model\/(\d+)/);
  return (
    <>
      <header>
        <a href="#/" className="brand">
          STL Library
        </a>
        <nav>
          <a href="#/">Library</a>
          <a href="#/collections">Collections</a>
          <a href="#/prints">Prints</a>
          <a href="#/health">Health</a>
          <a href="#/queue">Print queue</a>
          <a href="#/printer">Printer</a>
          <a href="#/imports">Imports</a>
          <a href="#/issues">Not following the convention</a>
          {admin && <a href="#/settings">Settings</a>}
          {admin && <a href="#/accounts">Accounts</a>}
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
          <Library />
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
