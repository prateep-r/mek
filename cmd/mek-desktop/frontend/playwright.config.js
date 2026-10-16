import { defineConfig } from "@playwright/test";

// The desktop app's window in a browser: test/desktop/uiserver serves the
// built frontend with the real internal/desktop service behind it, and a
// stub mek. Locally it uses your Chrome; CI installs Playwright's Chromium.
export default defineConfig({
  testDir: "e2e",
  fullyParallel: false,
  workers: 1,
  reporter: process.env.CI ? "github" : "list",
  use: {
    baseURL: "http://127.0.0.1:18767",
    channel: process.env.CI ? undefined : "chrome",
    viewport: { width: 1180, height: 760 },
  },
  webServer: {
    command: "go run ../../../test/desktop/uiserver -dist dist -addr 127.0.0.1:18767",
    url: "http://127.0.0.1:18767/e2e/log",
    reuseExistingServer: false,
    timeout: 120_000,
  },
});
