/**
 * The Open WebUI tab's engine sections (#449, #450, #451): the three admin
 * settings of Open WebUI that this server can answer — web search, reranking
 * and content extraction — with the settings path, the values to enter and
 * the Features module each needs. `httpBase` is this server's HTTP API URL on
 * the Server Domain (urlOnHost), so the URLs are the ones Open WebUI dials.
 *
 * Kept apart from llm-connection-templates.js so the tab's other examples can
 * change without touching these.
 */
import { featureLabel } from './features';

const KEY = 'API key: an MDDB API key (API Keys tab), sent as Bearer';

/** openWebUIEngineSections lists `{ id, title, steps }`, one per Open WebUI setting. */
export function openWebUIEngineSections(httpBase) {
  // The module by its Features page name, so the two never drift apart.
  const module = (name) => `Switch on "${featureLabel(name)}" under Features first; while it is off the endpoint answers 404`;
  return [
    {
      id: 'webSearch',
      title: 'Web search: Admin Panel → Settings → Web Search',
      steps: [
        'Web Search Engine: external',
        `External Web Search URL: ${httpBase}/v1/web-search (add ?collections=docs,kb to search only those)`,
        `External Web Search ${KEY}`,
        'Bypass Web Loader: on (the snippet is the passage; a link to MDDB needs the key)',
        module('webSearch'),
      ],
    },
    {
      id: 'rerankApi',
      title: 'Reranking: Admin Panel → Settings → Documents (Hybrid Search on)',
      steps: [
        'Reranking Engine: External',
        `API Base URL: ${httpBase}/v1/rerank`,
        KEY,
        'Reranking Model: reranker (any name; this server uses its own reranking model)',
        module('rerankApi'),
      ],
    },
    {
      id: 'extractApi',
      title: 'Content extraction: Admin Panel → Settings → Documents',
      steps: [
        'Content Extraction Engine: External',
        `Document Loader URL: ${httpBase}/v1/extract (Open WebUI adds /process)`,
        KEY,
        module('extractApi'),
      ],
    },
  ];
}
