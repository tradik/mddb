// Vitest configuration for the panel: one runner for every suite.
//
// - `*.test.js` files written for `node:test` run unchanged through the
//   alias below (see test/node-test-shim.js).
// - Component suites (`*.test.jsx`) opt into a DOM with a
//   `// @vitest-environment jsdom` docblock; everything else runs in Node.
// - Coverage is measured per file: every file in `coverageFiles` must keep at
//   least 98% of its lines, statements, functions and branches covered. Add a
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
  // 2.19.0: run a sync or backup now, and its last result (#377).
  'src/components/CronRunNow.jsx',
  // 2.19.0: the panel reaches the 2.18.2–2.19.0 features it lacked —
  // upload through the server's frontmatter parser, a collection's sources,
  // aliases, evaluation, storage, indexing status, async vector reindex,
  // code symbols and image documents.
  'src/lib/upload-files.js',
  'src/lib/eval-format.js',
  'src/lib/storage-format.js',
  'src/lib/use-reindex-jobs.js',
  'src/lib/code-symbol.js',
  'src/lib/image-meta.js',
  'src/components/UploadModal.jsx',
  'src/components/CollectionSources.jsx',
  'src/components/AliasesPanel.jsx',
  'src/components/EvalPanel.jsx',
  'src/components/StorageCard.jsx',
  'src/components/IndexingStatusCard.jsx',
  'src/components/ReindexJobsTable.jsx',
  'src/components/CodeSymbolPanel.jsx',
  'src/components/ImagePreview.jsx',
  // 2.19.0: files touched by the panel catch-up that had no tests.
  'src/App.jsx',
  'src/components/Sidebar.jsx',
  'src/components/DocumentViewer.jsx',
  // 2.19.0: chat scenarios stored in mddbd and edited here (#403).
  'src/lib/chat-scenario.js',
  'src/components/ChatScenariosPanel.jsx',
  'src/components/ChatScenarioForm.jsx',
  'src/components/ChatScenarioStyleFields.jsx',
  'src/components/ChatScenarioLlmFields.jsx',
  'src/components/ChatScenarioPreview.jsx',
  // 2.19.0: every admin feature of the server reachable from the panel —
  // agent memory, schemas, bulk import, personal API keys, facets,
  // duplicates, imports, batch writes, maintenance and the unused client
  // calls (classify, code graph, TTL, geo tools, temporal events, spell cleanup).
  'src/lib/client/http.js',
  'src/lib/client/index.js',
  'src/lib/client/memory.js',
  'src/lib/client/schemas.js',
  'src/lib/client/bulk-ingest.js',
  'src/lib/client/api-keys.js',
  'src/lib/client/aggregate.js',
  'src/lib/client/duplicates.js',
  'src/lib/client/batch.js',
  'src/lib/client/imports.js',
  'src/lib/client/maintenance.js',
  'src/lib/json-text.js',
  'src/lib/use-job-polling.js',
  'src/components/ConfirmDialog.jsx',
  'src/components/JsonTextarea.jsx',
  'src/components/ApiErrorNotice.jsx',
  'src/components/CollectionPicker.jsx',
  'src/components/OneTimeSecret.jsx',
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
        reporter: ['text', 'html', 'json-summary', 'lcov'],
        thresholds: {
          perFile: true,
          lines: 98,
          statements: 98,
          functions: 98,
          branches: 98,
        },
      },
    },
  }),
);
