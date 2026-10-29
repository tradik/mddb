// Flat ESLint config (ESLint 9+) for the mddb-panel Vite + React app.
//
// The project is plain JavaScript/JSX (no TypeScript), so no typescript-eslint
// layer is needed — just the JS recommended baseline plus the React rule sets.
import js from '@eslint/js';
import globals from 'globals';
import react from 'eslint-plugin-react';
import reactHooks from 'eslint-plugin-react-hooks';
import reactRefresh from 'eslint-plugin-react-refresh';

export default [
  { ignores: ['dist/**', 'build/**', 'coverage/**', 'node_modules/**'] },

  js.configs.recommended,

  // Browser-side React source (components, hooks, stores).
  {
    files: ['src/**/*.{js,jsx}'],
    languageOptions: {
      ecmaVersion: 2023,
      sourceType: 'module',
      globals: { ...globals.browser },
      parserOptions: { ecmaFeatures: { jsx: true } },
    },
    plugins: { react, 'react-hooks': reactHooks, 'react-refresh': reactRefresh },
    settings: { react: { version: 'detect' } },
    rules: {
      ...react.configs.flat.recommended.rules,
      ...reactHooks.configs['recommended-latest'].rules,
      // New JSX transform (React 19) — React need not be in scope.
      'react/react-in-jsx-scope': 'off',
      // This app does not use prop-types.
      'react/prop-types': 'off',
      // Cosmetic (literal ' / " in JSX text render fine) — not a correctness rule.
      'react/no-unescaped-entities': 'off',
      // Missing-dependency hints are valuable but need human judgement to fix
      // safely (adding deps can change effect behaviour), so keep them advisory.
      'react-hooks/exhaustive-deps': 'warn',
      // eslint-plugin-react-hooks 7 ships React-Compiler-derived rules that
      // flag long-standing patterns (module-scope components, in-place
      // mutation, setState inside effects). Fixing them safely is a per-case
      // refactor, so — like exhaustive-deps above — they stay advisory.
      'react-hooks/static-components': 'warn',
      'react-hooks/immutability': 'warn',
      'react-hooks/set-state-in-effect': 'warn',
      'no-unused-vars': [
        'error',
        {
          argsIgnorePattern: '^_',
          varsIgnorePattern: '^_',
          caughtErrorsIgnorePattern: '^_',
          // e.g. react-markdown `components={{ h1: ({ node, ...props }) => ... }}`
          ignoreRestSiblings: true,
        },
      ],
      'react-refresh/only-export-components': ['warn', { allowConstantExport: true }],
    },
  },

  // Node-side files: dev/build tooling, the SSR server, tests and test helpers.
  {
    files: ['*.js', '**/*.test.js', 'test/**/*.js'],
    languageOptions: {
      ecmaVersion: 2023,
      sourceType: 'module',
      globals: { ...globals.node },
    },
  },
];
