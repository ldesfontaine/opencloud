import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";

// En développement, l'API et les routes de l'agent passent au serveur Go
// (make run, port 8080) ; en production Vite ne sert rien, dist/ est embarqué.
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      "/api": "http://127.0.0.1:8080",
      "/agent": "http://127.0.0.1:8080",
      "/ping": "http://127.0.0.1:8080",
    },
  },
  build: {
    outDir: "dist",
    // Le Makefile vide dist/ lui-même et garde .gitkeep : go:embed exige un dossier non vide.
    emptyOutDir: false,
    sourcemap: false,
  },
  test: {
    include: ["src/**/*.test.ts"],
  },
});
