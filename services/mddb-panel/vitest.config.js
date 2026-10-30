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
  // 2.18.10: the panel catches up with 2.18.9/2.18.10 server features.
  'src/lib/limit-notice.js',
  'src/lib/search-options.js',
  'src/lib/download.js',
  'src/lib/permissions.js',
  'src/lib/automation-form.js',
  'src/lib/collection-config.js',
  'src/components/Header.jsx',
  'src/components/LimitCappedNotice.jsx',
  'src/components/SearchPlan.jsx',
  'src/components/SearchPlanningControls.jsx',
  'src/components/VectorSearchPanel.jsx',
  'src/components/HybridSearchPanel.jsx',
  'src/components/CrossSearchPanel.jsx',
  'src/components/VectorPanel.jsx',
  'src/components/IndexTrainingNotice.jsx',
  'src/components/SearchAdvisorPanel.jsx',
  'src/components/CorpusPacksCard.jsx',
  'src/components/DiagnoseCard.jsx',
  'src/components/ReadOnlyNotice.jsx',
  'src/components/DocumentList.jsx',
  'src/components/CollectionConfigModal.jsx',
  'src/components/CollectionAdvancedSettings.jsx',
  'src/components/AutomationPanel.jsx',
  'src/components/AutomationLogsTab.jsx',
  'src/components/CronActionFields.jsx',
  'src/components/PermissionScopeHint.jsx',
  'src/components/UsersPanel.jsx',
  'src/components/GroupsPanel.jsx',
  // 2.18.10: the last server/panel gaps (config report, quota usage, a11y).
  'src/lib/abort-slot.js',
  'src/lib/quota-usage.js',
  'src/lib/server-edge.js',
  'src/components/QuotaUsage.jsx',
  'src/components/ServerEdgeCard.jsx',
  'src/components/ConfigPanel.jsx',
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
