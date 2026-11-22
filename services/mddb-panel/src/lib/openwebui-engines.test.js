// openwebui-engines (#449-#451): the Open WebUI tab's three engine sections.
import { describe, test, expect } from 'vitest';
import { openWebUIEngineSections } from './openwebui-engines';
import { FEATURE_LABELS } from './features';

describe('openWebUIEngineSections', () => {
  const sections = openWebUIEngineSections('https://db.example.com');

  test('one section per module, each naming its Open WebUI settings path', () => {
    expect(sections.map((s) => s.id)).toEqual(['webSearch', 'rerankApi', 'extractApi']);
    for (const s of sections) {
      expect(s.title).toMatch(/Admin Panel → Settings → (Web Search|Documents)/);
      expect(s.steps.some((step) => step.includes('MDDB API key'))).toBe(true);
    }
  });

  test('the URLs are on this server, the loader one without /process', () => {
    const urls = sections.flatMap((s) => s.steps).filter((step) => step.includes('https://'));
    expect(urls).toEqual([
      'External Web Search URL: https://db.example.com/v1/web-search (add ?collections=docs,kb to search only those)',
      'API Base URL: https://db.example.com/v1/rerank',
      'Document Loader URL: https://db.example.com/v1/extract (Open WebUI adds /process)',
    ]);
  });

  test('each says which Features module to switch on, by its Features page label', () => {
    for (const s of sections) {
      expect(s.steps.at(-1)).toContain(`Switch on "${FEATURE_LABELS[s.id]}" under Features`);
    }
  });
});
