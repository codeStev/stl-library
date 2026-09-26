import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// The build lands where the Go server embeds it. In development, API calls
// go to a running `stlib serve`.
export default defineConfig({
  plugins: [react()],
  // The 3D viewer chunk (three.js) is ~600 kB and loads only when opened.
  build: { outDir: "../internal/adapters/web/dist", emptyOutDir: true, chunkSizeWarningLimit: 700 },
  server: { proxy: { "/api": "http://127.0.0.1:8080" } },
});
