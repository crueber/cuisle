import { defineConfig } from "vitest/config";
import preact from "@preact/preset-vite";

export default defineConfig({
  plugins: [preact()],
  test: {
    environment: "jsdom",
    globals: true,
    include: ["src/**/*.test.{ts,tsx}", "scripts/**/*.test.{js,mjs,ts}"],
    setupFiles: ["src/test/setup.ts"],
    restoreMocks: true,
  },
});
