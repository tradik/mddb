// Vitest configuration for the panel: one runner for every suite.
//
// - `*.test.js` files written for `node:test` run unchanged through the
//   alias below (see test/node-test-shim.js).
// - Component suites (`*.test.jsx`) opt into a DOM with a
//   `// @vitest-environment jsdom` docblock; everything else runs in Node.
// - Coverage is measured per file: every file in `coverageFiles` must keep at
//   least 97% of its lines, statements, functions and branches covered. Add a
//   file here when a release touches it.
import { fileURLToPath } from 'node:url';
import { defineConfig, mergeConfig } from 'vitest/config';
import viteConfig from './vite.config.js';

const coverageFiles = [
  'src/lib/mddb-client.js',
  'src/lib/gain-format.js',
  'src/components/GainCard.jsx',
  'src/components/SystemInfo.jsx',
  'src/components/AuditLogPanel.jsx',
];

export default mergeConfig(
  viteConfig,
  defineConfig({
    resolve: {
      alias: {
        'node:test': fileURLToPath(new URL('./test/node-test-shim.js', import.meta.url)),
      },
    },
    test: {
      include: ['*.test.js', 'src/**/*.test.{js,jsx}'],
      environment: 'node',
      restoreMocks: true,
      unstubGlobals: true,
      coverage: {
        provider: 'v8',
        include: coverageFiles,
        reporter: ['text', 'html', 'json-summary'],
        thresholds: {
          perFile: true,
          lines: 97,
          statements: 97,
          functions: 97,
          branches: 97,
        },
      },
    },
  }),
);
