// Lets the existing `node:test` suites run unchanged under Vitest.
//
// vitest.config.js aliases `node:test` to this module, so a file written as
//   import { test } from 'node:test';
// registers its cases with Vitest instead of Node's runner. The same files
// still run under `node --test <file>`, because Node never sees the alias.
// Only the API those suites use is mapped; add names here if a suite needs more.
export { test, describe, it, beforeEach, afterEach, beforeAll as before, afterAll as after } from 'vitest';
