import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";

// The production build is embedded into the Go binary (app/web/dist).
export default defineConfig({
  plugins: [react()],
  build: { outDir: "../app/web/dist", emptyOutDir: false, sourcemap: false },
  server: { proxy: { "/api": "http://127.0.0.1:8081" } },
  test: { environment: "node" },
});
