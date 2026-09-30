import { useEffect, useState } from "react";
import { api, formatDuration, type SliceMeta } from "./api";

// PlateInfo shows what a sliced file says about itself: its preview picture, the layers,
// print time and resin. Files that aren't readable sliced files show nothing.
export function PlateInfo({ kind, id }: { kind: "part" | "upload"; id: number | string }) {
  const [meta, setMeta] = useState<SliceMeta | null>(null);
  const [big, setBig] = useState(false);
  useEffect(() => {
    let live = true;
    api.sliceMeta(kind, id).then((m) => live && setMeta(m), () => {});
    return () => {
      live = false;
    };
  }, [kind, id]);
  if (!meta) return null;
  const bits = [
    `${meta.layers} layers`,
    `${+meta.layerHeight.toFixed(3)} mm`,
    meta.printSeconds ? formatDuration(meta.printSeconds) : "",
    meta.volumeMl ? `${meta.volumeMl.toFixed(1)} ml` : "",
    meta.exposureS ? `${meta.exposureS} s exposure` : "",
  ].filter(Boolean);
  return (
    <div className="plate-info">
      {meta.hasPreview && (
        <img className={big ? "plate-thumb big" : "plate-thumb"} src={api.slicePreviewURL(kind, id)} alt="Plate preview" loading="lazy" onClick={() => setBig(!big)} title="click to enlarge" />
      )}
      <span className="muted">{bits.join(" · ")}</span>
    </div>
  );
}
