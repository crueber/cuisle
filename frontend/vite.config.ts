import { defineConfig } from "vite";
import preact from "@preact/preset-vite";
import wails from "@wailsio/runtime/plugins/vite";

// `vite --mode mock` (and `vite build --mode mock`) runs the UI in a plain browser against src/api/mock.ts.
export default defineConfig(({ mode }) => ({
  plugins: [preact(), ...(mode === "mock" ? [] : [wails("./src/bindings")])],
  server: {
    host: "127.0.0.1",
    port: Number(process.env.WAILS_VITE_PORT) || 9245,
    strictPort: true,
  },
  build: {
    target: "safari15", // WebKitGTK 2.40 / WKWebView baseline (architecture R8)
    cssTarget: "safari15",
    assetsInlineLimit: 0,
    sourcemap: false,
    modulePreload: false,
    // Stable names so size-limit can budget the entry bundle apart from lazy chunks (the palette; architecture §6.1).
    rollupOptions: {
      output: {
        entryFileNames: "assets/index-[hash].js",
        chunkFileNames: "assets/lazy-[name]-[hash].js",
        // The entry's CSS stays index-*.css; a lazy chunk's CSS becomes lazy-*.css.
        assetFileNames: (a) => (a.names.some((n) => n.endsWith(".css") && n !== "index.css") ? "assets/lazy-[name]-[hash][extname]" : "assets/[name]-[hash][extname]"),
      },
    },
    // terser packs ~0.5 KB gz tighter than the default minifier (architecture §6.1 JS budget).
    minify: "terser",
    terserOptions: {
      ecma: 2020,
      module: true,
      toplevel: true,
      compress: { passes: 3 },
      mangle: { toplevel: true },
      format: { comments: false },
    },
  },
}));
