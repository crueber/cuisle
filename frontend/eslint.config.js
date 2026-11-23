import js from "@eslint/js";
import tseslint from "typescript-eslint";
import globals from "globals";

export default tseslint.config(
  { ignores: ["dist/**", "dist-mock/**", "src/bindings/**", "node_modules/**", "test-results/**", "playwright-report/**"] },
  js.configs.recommended,
  ...tseslint.configs.recommended,
  {
    languageOptions: { globals: { ...globals.browser, ...globals.node } },
    rules: {
      "@typescript-eslint/no-unused-vars": ["error", { argsIgnorePattern: "^_", varsIgnorePattern: "^_" }],
      "no-restricted-imports": ["error", {
        patterns: [{ group: ["**/bindings/**", "**/bindings"], message: "Import application types and calls from src/api only (architecture §4.4)." }],
        paths: [{ name: "react", message: "Use preact." }],
      }],
      "no-restricted-syntax": ["error",
        { selector: "JSXOpeningElement[name.name='select']", message: "Use the shared Combobox (AGENTS.md invariant 4)." },
        { selector: "CallExpression[callee.name='setInterval']", message: "No polling or tickers (AGENTS.md invariant 2)." },
        { selector: "CallExpression[callee.name='eval']", message: "No eval." },
      ],
    },
  },
  {
    files: ["src/api/**"],
    rules: { "no-restricted-imports": "off" },
  },
  {
    files: ["src/components/**", "src/views/**"],
    rules: {
      "no-restricted-syntax": ["error",
        { selector: "JSXOpeningElement[name.name='select']", message: "Use the shared Combobox (AGENTS.md invariant 4)." },
        { selector: "CallExpression[callee.name='setInterval']", message: "No polling or tickers (AGENTS.md invariant 2)." },
        { selector: "Literal[value=/#[0-9a-fA-F]{3,8}\\b/]", message: "Tokens only: no raw hex colours (AGENTS.md invariant 11)." },
      ],
    },
  },
);
