import { STLLoader } from "three/examples/jsm/loaders/STLLoader.js";

// Parses an STL off the main thread, so a big file never freezes the page.
const ctx = self as unknown as {
  onmessage: ((e: MessageEvent<ArrayBuffer>) => void) | null;
  postMessage: (m: unknown, t?: Transferable[]) => void;
};

ctx.onmessage = (e) => {
  try {
    const geo = new STLLoader().parse(e.data);
    const pos = geo.getAttribute("position").array as Float32Array;
    const nor = geo.getAttribute("normal")?.array as Float32Array | undefined;
    const transfer: Transferable[] = [pos.buffer];
    if (nor) transfer.push(nor.buffer);
    ctx.postMessage({ pos, nor }, transfer);
  } catch (err) {
    ctx.postMessage({ error: String(err) });
  }
};
