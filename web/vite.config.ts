import { defineConfig, loadEnv } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), "");
  const apiTarget = env.VITE_NIUMA_API_TARGET || env.VITE_GO_CLAUDE_API_TARGET || "http://127.0.0.1:8080";

  return {
    base: mode === "production" ? "/webui/" : "/",
    define: {
      "import.meta.env.VITE_DESKTOP_UI_VERSION": JSON.stringify(env.VITE_DESKTOP_UI_VERSION || "")
    },
    plugins: [react()],
    server: {
      port: 5173,
      proxy: {
        "/api": {
          target: apiTarget,
          changeOrigin: true,
          rewrite: (path) => path.replace(/^\/api/, "")
        }
      }
    },
    test: {
      environment: "jsdom",
      globals: true,
      exclude: ["e2e/**", "node_modules/**", "dist/**"]
    }
  };
});
