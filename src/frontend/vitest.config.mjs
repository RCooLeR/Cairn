import { defineConfig } from "vitest/config";

export default defineConfig({
  test: {
    environment: "jsdom",
    exclude: ["e2e/**", "dist/**", "node_modules/**"],
    globals: true,
    setupFiles: "./src/test/setup.ts",
  },
});
