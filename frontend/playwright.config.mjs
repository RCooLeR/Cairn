import { defineConfig, devices } from "@playwright/test";

const port = Number(process.env.CAIRN_RELEASE_UI_PORT) || 4173;
const baseURL = `http://127.0.0.1:${port}`;

export default defineConfig({
  testDir: "./e2e",
  outputDir: "./test-results/release-ui",
  fullyParallel: false,
  reporter: process.env.CI ? [["dot"], ["html", { open: "never" }]] : "list",
  timeout: 60_000,
  expect: {
    timeout: 10_000,
  },
  use: {
    ...devices["Desktop Chrome"],
    baseURL,
    colorScheme: "light",
    reducedMotion: "reduce",
    trace: "retain-on-failure",
    viewport: { width: 1440, height: 960 },
  },
  webServer: {
    command: `npm run build:release-ui && npm run preview -- --host 127.0.0.1 --port ${port}`,
    env: {
      CAIRN_BROWSER_MOCKS: "1",
      VITE_CAIRN_VERSION: "1.0.0",
    },
    reuseExistingServer: !process.env.CI,
    timeout: 120_000,
    url: baseURL,
  },
  projects: [
    {
      name: "chromium",
      use: { browserName: "chromium" },
    },
  ],
});
