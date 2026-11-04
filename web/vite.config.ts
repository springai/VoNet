import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  build: {
    outDir: "dist",
    emptyOutDir: true,
    sourcemap: false,
    chunkSizeWarningLimit: 1100,
    rollupOptions: {
      output: {
        manualChunks(id) {
          if (id.indexOf("/node_modules/echarts/") >= 0 || id.indexOf("/node_modules/zrender/") >= 0) {
            return "echarts";
          }
          if (
            id.indexOf("/node_modules/react/") >= 0 ||
            id.indexOf("/node_modules/react-dom/") >= 0 ||
            id.indexOf("/node_modules/react-router") >= 0
          ) {
            return "react-vendor";
          }
          if (id.indexOf("/node_modules/@fluentui/react-icons/") >= 0) {
            return "icons";
          }
          return undefined;
        },
      },
    },
  },
  server: {
    port: 5173,
    proxy: {
      "/api": "http://127.0.0.1:7575",
    },
  },
});
