import { defineConfig, devices } from "@playwright/test";

// e2e, perf, a11y and screenshot tests run against `vite preview --mode mock` (architecture §6.1, testing §5).
export default defineConfig({
  testDir: "tests",
  fullyParallel: true,
  reporter: [["list"]],
  use: { baseURL: process.env.PLAYWRIGHT_BASE_URL ?? "http://127.0.0.1:4173", ...devices["Desktop Chrome"] },
  webServer: process.env.PLAYWRIGHT_BASE_URL ? undefined : {
    // Local binaries, not `npm run` (some sandboxes block it), and never a reused server: a preview left running from
    // an earlier build would serve stale assets and the screenshots would test old code.
    command:
      "./node_modules/.bin/vite build --mode mock --outDir dist-mock && " +
      "./node_modules/.bin/vite preview --mode mock --outDir dist-mock --host 127.0.0.1 --port 4173 --strictPort",
    url: "http://127.0.0.1:4173",
    reuseExistingServer: false,
    timeout: 120_000,
  },
  snapshotPathTemplate: "{testDir}/__screenshots__/{testFilePath}/{arg}-{platform}{ext}",
});
