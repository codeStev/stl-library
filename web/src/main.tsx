import { StrictMode, useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import { Library } from "./Library";
import { ModelPage } from "./ModelPage";
import { Issues } from "./Issues";
import { Queue } from "./Queue";
import { Imports } from "./Imports";
import { Printer } from "./Printer";
import { Settings } from "./Settings";
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

function App() {
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
          <a href="#/queue">Print queue</a>
          <a href="#/printer">Printer</a>
          <a href="#/imports">Imports</a>
          <a href="#/issues">Not following the convention</a>
          <a href="#/settings">Settings</a>
        </nav>
      </header>
      <main>
        {model ? (
          <ModelPage id={Number(model[1])} />
        ) : route.startsWith("#/issues") ? (
          <Issues />
        ) : route.startsWith("#/queue") ? (
          <Queue />
        ) : route.startsWith("#/imports") ? (
          <Imports />
        ) : route.startsWith("#/printer") ? (
          <Printer />
        ) : route.startsWith("#/settings") ? (
          <Settings />
        ) : (
          <Library />
        )}
      </main>
    </>
  );
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
