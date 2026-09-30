import { STLLoader } from "three/examples/jsm/loaders/STLLoader.js";

// Parses an STL off the main thread, so a big file never freezes the page.
const ctx = self as unknown as {
  onmessage: ((e: MessageEvent<ArrayBuffer>) => void) | null;
  postMessage: (m: unknown, t?: Transferable[]) => void;
};

ctx.onmessage = (e) => {
  try {
    const geo = new STLLoader().parse(e.data);
    // The normals written in the file are often zero or wrong: work them out from the triangles.
    geo.computeVertexNormals();
    const pos = geo.getAttribute("position").array as Float32Array;
    const nor = geo.getAttribute("normal").array as Float32Array;
    ctx.postMessage({ pos, nor }, [pos.buffer, nor.buffer]);
  } catch (err) {
    ctx.postMessage({ error: String(err) });
  }
};
