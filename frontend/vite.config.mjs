import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import path from "node:path";
import { fileURLToPath } from "node:url";

const rootDir = path.dirname(fileURLToPath(import.meta.url));
const runtimeMock = path.resolve(rootDir, "src/test/wailsRuntimeMock.ts");

function wailsRuntimePlugin(browserMocks) {
  return {
    name: "cairn-wails-runtime",
    enforce: "pre",
    resolveId(id) {
      if (id !== "@wailsio/runtime") {
        return undefined;
      }
      if (browserMocks) {
        return runtimeMock;
      }
      return {
        id: "/wails/runtime.js",
        external: true,
      };
    },
  };
}

export default defineConfig(({ mode }) => {
  const browserMocks =
    mode === "release-validation" || process.env.CAIRN_BROWSER_MOCKS === "1";

  return {
    server: {
      host: "127.0.0.1",
      port: Number(process.env.WAILS_VITE_PORT) || 9245,
      strictPort: true,
    },
    plugins: [wailsRuntimePlugin(browserMocks), react()],
  };
});
