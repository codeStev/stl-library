import { useEffect, useRef, useState } from "react";
import * as THREE from "three";
import { OrbitControls } from "three/examples/jsm/controls/OrbitControls.js";
import { OBJLoader } from "three/examples/jsm/loaders/OBJLoader.js";
import { ThreeMFLoader } from "three/examples/jsm/loaders/3MFLoader.js";
import { formatBytes } from "./api";

// Parses an STL in a worker (off the main thread).
function parseSTL(buf: ArrayBuffer): Promise<THREE.BufferGeometry> {
  return new Promise((resolve, reject) => {
    const worker = new Worker(new URL("./stlWorker.ts", import.meta.url), { type: "module" });
    worker.onmessage = (e: MessageEvent<{ pos?: Float32Array; nor?: Float32Array; error?: string }>) => {
      worker.terminate();
      if (e.data.error || !e.data.pos) return reject(new Error(e.data.error ?? "empty"));
      const geo = new THREE.BufferGeometry();
      geo.setAttribute("position", new THREE.BufferAttribute(e.data.pos, 3));
      if (e.data.nor) geo.setAttribute("normal", new THREE.BufferAttribute(e.data.nor, 3));
      else geo.computeVertexNormals();
      resolve(geo);
    };
    worker.onerror = (e) => {
      worker.terminate();
      reject(new Error(e.message));
    };
    worker.postMessage(buf, [buf]);
  });
}

// Downloads with progress.
async function download(url: string, size: number, signal: AbortSignal, progress: (s: string) => void): Promise<ArrayBuffer> {
  const res = await fetch(url, { signal });
  if (!res.ok) throw new Error(`${res.status}`);
  const total = Number(res.headers.get("Content-Length")) || size;
  if (!res.body) return res.arrayBuffer();
  const reader = res.body.getReader();
  const chunks: Uint8Array[] = [];
  let got = 0;
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    chunks.push(value);
    got += value.length;
    progress(`Loading ${total ? Math.round((got / total) * 100) + "%" : formatBytes(got)} (${formatBytes(got)})…`);
  }
  const out = new Uint8Array(got);
  let o = 0;
  for (const c of chunks) {
    out.set(c, o);
    o += c.length;
  }
  return out.buffer;
}

export interface ViewerProps {
  url: string;
  name: string;
  size: number;
  onClose?: () => void;
  // Given, the viewer offers "Use this view as the preview" and calls this with a PNG of the current view.
  onSetPreview?: (png: Blob) => Promise<unknown>;
  // A view inside the page (not a full-screen dialog); it keeps rendering only while something changes.
  inline?: boolean;
  // With onSetPreview: the Enter key does the same as the button (for going through many models).
  hotkey?: boolean;
}

// A 3D view of one part file. This module (and three.js) is loaded only when a viewer is opened.
export default function Viewer({ url, name, size, onClose, onSetPreview, inline = false, hotkey = false }: ViewerProps) {
  const mount = useRef<HTMLDivElement>(null);
  const capture = useRef<() => Promise<Blob | null>>(async () => null);
  const [status, setStatus] = useState(`Loading ${formatBytes(size)}…`);
  const [saved, setSaved] = useState("");

  useEffect(() => {
    const el = mount.current!;
    const renderer = new THREE.WebGLRenderer({ antialias: size < 60 << 20, alpha: true });
    renderer.setPixelRatio(Math.min(devicePixelRatio, 1.5));
    renderer.setSize(el.clientWidth, el.clientHeight);
    el.appendChild(renderer.domElement);

    const scene = new THREE.Scene();
    const camera = new THREE.PerspectiveCamera(35, el.clientWidth / el.clientHeight, 0.1, 10000);
    scene.add(new THREE.HemisphereLight(0xffffff, 0x444450, 1.6));
    const key = new THREE.DirectionalLight(0xffffff, 2.2);
    camera.add(key); // light follows the camera, so every side is lit
    key.position.set(1, 1.5, 2);
    scene.add(camera);
    const controls = new OrbitControls(camera, renderer.domElement);
    controls.enableDamping = true;

    const material = new THREE.MeshStandardMaterial({ color: 0xb2bac4, roughness: 0.6, metalness: 0.05, flatShading: false });
    const abort = new AbortController();
    let disposed = false;
    let dirty = true;

    const frame = (obj: THREE.Object3D) => {
      // Print files are Z-up; three.js is Y-up.
      obj.rotation.x = -Math.PI / 2;
      const box = new THREE.Box3().setFromObject(obj);
      const center = box.getCenter(new THREE.Vector3());
      const radius = box.getSize(new THREE.Vector3()).length() / 2 || 1;
      obj.position.sub(center);
      camera.near = radius / 100;
      camera.far = radius * 100;
      camera.position.set(radius * 1.6, radius * 1.0, radius * 2.2);
      camera.updateProjectionMatrix();
      controls.target.set(0, 0, 0);
      controls.update();
      scene.add(obj);
      dirty = true;
    };

    (async () => {
      try {
        const buf = await download(url, size, abort.signal, (s) => !disposed && setStatus(s));
        if (disposed) return;
        setStatus("Preparing…");
        const ext = name.split(".").pop()!.toLowerCase();
        let obj: THREE.Object3D;
        if (ext === "stl") {
          obj = new THREE.Mesh(await parseSTL(buf), material);
        } else if (ext === "obj") {
          obj = new OBJLoader().parse(new TextDecoder().decode(buf));
          obj.traverse((c) => {
            if (c instanceof THREE.Mesh) c.material = material;
          });
        } else {
          obj = new ThreeMFLoader().parse(buf);
        }
        if (disposed) return;
        frame(obj);
        setStatus("");
      } catch (e) {
        if (!disposed) setStatus(`Could not load: ${e}`);
      }
    })();

    // Render only when the view changed: an idle viewer costs nothing.
    let raf = 0;
    const tick = () => {
      const moved = controls.update(); // true while the view changes (also while damping settles)
      if (moved || dirty) {
        renderer.render(scene, camera);
        dirty = false;
      }
      raf = requestAnimationFrame(tick);
    };
    tick();

    // A PNG of what is on screen now (transparent background), at most 640 px a side.
    capture.current = async () => {
      renderer.render(scene, camera); // in this task, so the canvas still holds the picture
      const src = renderer.domElement;
      const scale = Math.min(1, 640 / Math.max(src.width, src.height));
      const out = document.createElement("canvas");
      out.width = Math.max(16, Math.round(src.width * scale));
      out.height = Math.max(16, Math.round(src.height * scale));
      out.getContext("2d")!.drawImage(src, 0, 0, out.width, out.height);
      return new Promise((resolve) => out.toBlob(resolve, "image/png"));
    };

    const onResize = () => {
      camera.aspect = el.clientWidth / el.clientHeight;
      camera.updateProjectionMatrix();
      renderer.setSize(el.clientWidth, el.clientHeight);
      dirty = true;
    };
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose?.();
    addEventListener("resize", onResize);
    if (!inline) addEventListener("keydown", onKey);

    return () => {
      disposed = true;
      abort.abort();
      cancelAnimationFrame(raf);
      removeEventListener("resize", onResize);
      removeEventListener("keydown", onKey);
      controls.dispose();
      scene.traverse((o) => {
        if (o instanceof THREE.Mesh) o.geometry.dispose();
      });
      material.dispose();
      renderer.dispose();
      el.removeChild(renderer.domElement);
    };
  }, [url, name, size, inline, onClose]);

  const setPreview = async () => {
    setSaved("Saving…");
    try {
      const blob = await capture.current();
      if (!blob) throw new Error("could not take the picture");
      await onSetPreview!(blob);
      setSaved("Saved as the preview ✓");
    } catch (e) {
      setSaved(`Not saved: ${e}`);
    }
  };

  const latestSet = useRef(setPreview);
  latestSet.current = setPreview;
  const ready = !status;
  useEffect(() => {
    if (!hotkey || !onSetPreview || !ready) return;
    const on = (e: KeyboardEvent) => {
      const t = e.target as HTMLElement | null;
      if (e.key !== "Enter" || e.repeat || e.ctrlKey || e.metaKey || e.altKey || (t && /^(INPUT|TEXTAREA|SELECT|BUTTON|A)$/.test(t.tagName))) return;
      e.preventDefault();
      void latestSet.current();
    };
    addEventListener("keydown", on);
    return () => removeEventListener("keydown", on);
  }, [hotkey, onSetPreview, ready]);

  const bar = (
    <div className="viewer-bar">
      <span>{name}</span>
      {onSetPreview && !status && (
        <button onClick={setPreview} title="Turn the model to the view you like, then click">
          Use this view as the preview{hotkey ? " (Enter)" : ""}
        </button>
      )}
      {saved && <span className="muted">{saved}</span>}
      {!inline && onClose && (
        <button onClick={onClose} aria-label="Close">
          ✕
        </button>
      )}
    </div>
  );
  return (
    <div className={inline ? "viewer inline" : "viewer"} role={inline ? undefined : "dialog"} aria-label={`3D view of ${name}`}>
      {bar}
      <div className="viewer-canvas" ref={mount} />
      {status && <div className="viewer-status">{status}</div>}
    </div>
  );
}
