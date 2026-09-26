import { useEffect, useRef, useState } from "react";
import * as THREE from "three";
import { OrbitControls } from "three/examples/jsm/controls/OrbitControls.js";
import { STLLoader } from "three/examples/jsm/loaders/STLLoader.js";
import { OBJLoader } from "three/examples/jsm/loaders/OBJLoader.js";
import { ThreeMFLoader } from "three/examples/jsm/loaders/3MFLoader.js";
import { formatBytes } from "./api";

// An on-demand 3D view of one part file. This module (and three.js) is
// loaded only when a viewer is opened.
export default function Viewer({ url, name, size, onClose }: { url: string; name: string; size: number; onClose: () => void }) {
  const mount = useRef<HTMLDivElement>(null);
  const [status, setStatus] = useState(`Loading ${formatBytes(size)}…`);

  useEffect(() => {
    const el = mount.current!;
    const renderer = new THREE.WebGLRenderer({ antialias: true, alpha: true });
    renderer.setPixelRatio(Math.min(devicePixelRatio, 2));
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
    };

    (async () => {
      try {
        const res = await fetch(url, { signal: abort.signal });
        if (!res.ok) throw new Error(`${res.status}`);
        const buf = await res.arrayBuffer();
        if (disposed) return;
        setStatus("Preparing…");
        const ext = name.split(".").pop()!.toLowerCase();
        let obj: THREE.Object3D;
        if (ext === "stl") {
          const geo = new STLLoader().parse(buf);
          geo.computeVertexNormals();
          obj = new THREE.Mesh(geo, material);
        } else if (ext === "obj") {
          obj = new OBJLoader().parse(new TextDecoder().decode(buf));
          obj.traverse((c) => {
            if (c instanceof THREE.Mesh) c.material = material;
          });
        } else {
          obj = new ThreeMFLoader().parse(buf);
        }
        frame(obj);
        setStatus("");
      } catch (e) {
        if (!disposed) setStatus(`Could not load: ${e}`);
      }
    })();

    let raf = 0;
    const loop = () => {
      controls.update();
      renderer.render(scene, camera);
      raf = requestAnimationFrame(loop);
    };
    loop();

    const onResize = () => {
      camera.aspect = el.clientWidth / el.clientHeight;
      camera.updateProjectionMatrix();
      renderer.setSize(el.clientWidth, el.clientHeight);
    };
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    addEventListener("resize", onResize);
    addEventListener("keydown", onKey);

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
  }, [url, name, onClose]);

  return (
    <div className="viewer" role="dialog" aria-label={`3D view of ${name}`}>
      <div className="viewer-bar">
        <span>{name}</span>
        <button onClick={onClose} aria-label="Close">
          ✕
        </button>
      </div>
      <div className="viewer-canvas" ref={mount} />
      {status && <div className="viewer-status">{status}</div>}
    </div>
  );
}
