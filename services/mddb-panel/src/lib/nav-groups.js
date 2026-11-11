/**
 * The sidebar menu as data (2.19.0): six collapsible groups and the pages in
 * each. The Sidebar renders from this list, so placing a page is one line.
 *
 * An entry's `when` names a condition beyond its server module (GO-049,
 * features.js): `auth` (pages that exist only with authentication),
 * `cluster` (a server with replication) or `automations` (unless the server
 * reports automations disabled).
 */
import {
  Server, Settings, Network, Users, UsersIcon, Sliders, GitBranch, BookOpen, Ban, Zap, Globe, Shuffle,
  Clock, Type, Pin, FileSearch, Webhook, Shield, Lock, ScatterChart, Compass, Link2, Gauge, Code2,
  MessageSquare, ToggleRight, Brain, Database, MessagesSquare, FileCheck, Import, CopyX, UserRound,
} from 'lucide-react';
import { isViewEnabled } from './features';

export const NAV_GROUPS = [
  {
    id: 'search', label: 'Search', items: [
      { mode: 'vectors', icon: Database, label: 'Vector Search' },
      { mode: 'crossSearch', icon: Shuffle, label: 'Cross Search' },
      { mode: 'geo', icon: Globe, label: 'Geo Search' },
      { mode: 'vectorSpace', icon: ScatterChart, label: 'Vector Space' },
      { mode: 'searchAdvisor', icon: Compass, label: 'Search Advisor' },
      { mode: 'eval', icon: Gauge, label: 'Retrieval Evaluation' },
      { mode: 'codeSymbols', icon: Code2, label: 'Code Symbols' },
      { mode: 'synonyms', icon: BookOpen, label: 'Synonyms' },
      { mode: 'stopwords', icon: Ban, label: 'Stop Words' },
      { mode: 'curation', icon: Pin, label: 'Curation Rules' },
      { mode: 'spellcheck', icon: Type, label: 'Spell Checker' },
    ],
  },
  {
    id: 'data', label: 'Data', items: [
      { mode: 'aliases', icon: Link2, label: 'Collection Aliases' },
      { mode: 'schemas', icon: FileCheck, label: 'Collection Schemas' },
      { mode: 'embeddings', icon: Brain, label: 'Embedding Models' },
      { mode: 'import', icon: Import, label: 'Import' },
      { mode: 'duplicates', icon: CopyX, label: 'Duplicates' },
      { mode: 'temporal', icon: Clock, label: 'Temporal Analytics' },
    ],
  },
  {
    id: 'chat', label: 'Chat', items: [
      { mode: 'chat', icon: MessageSquare, label: 'Chat' },
      { mode: 'memory', icon: MessagesSquare, label: 'Agent Memory' },
      { mode: 'mcp', icon: Brain, label: 'LLM Connections' },
    ],
  },
  {
    id: 'integrations', label: 'Integrations', items: [
      { mode: 'automation', icon: Zap, label: 'Automation', when: 'automations' },
      { mode: 'webhooks', icon: Webhook, label: 'Webhooks' },
      { mode: 'endpoints', icon: Network, label: 'API Endpoints' },
    ],
  },
  {
    id: 'security', label: 'Security', items: [
      { mode: 'users', icon: Users, label: 'Users', when: 'auth' },
      { mode: 'groups', icon: UsersIcon, label: 'Groups', when: 'auth' },
      { mode: 'account', icon: UserRound, label: 'My Account', when: 'auth' },
      { mode: 'security', icon: Shield, label: 'Security' },
      { mode: 'encryption', icon: Lock, label: 'Encryption' },
      { mode: 'auditLog', icon: FileSearch, label: 'Audit Log' },
    ],
  },
  {
    id: 'system', label: 'System', items: [
      { mode: 'system', icon: Server, label: 'System Info' },
      { mode: 'cluster', icon: GitBranch, label: 'Cluster', when: 'cluster' },
      { mode: 'config', icon: Settings, label: 'Configuration' },
      { mode: 'features', icon: ToggleRight, label: 'Features' },
      { mode: 'settings', icon: Sliders, label: 'Client Settings' },
    ],
  },
];

/** Lower case without diacritics, so "zrodla" finds "Źródła". */
export function foldText(text) {
  return String(text).normalize('NFD').replace(/\p{Diacritic}/gu, '').toLowerCase();
}

/**
 * The menu entries the server and the user allow. `context` is
 * `{ features, authEnabled, clusterEnabled, automationsEnabled }`.
 */
export function isEntryAvailable(item, context) {
  const conditions = { auth: context.authEnabled, cluster: context.clusterEnabled, automations: context.automationsEnabled };
  return (item.when === undefined || !!conditions[item.when]) && isViewEnabled(item.mode, context.features);
}

/**
 * The groups to show: each with its available entries matching `query`
 * (by label, case and diacritics aside); a group left empty is dropped.
 */
export function visibleGroups(context, query = '', groups = NAV_GROUPS) {
  const needle = foldText(query.trim());
  return groups
    .map((group) => ({
      ...group,
      items: group.items.filter((item) => isEntryAvailable(item, context) && foldText(item.label).includes(needle)),
    }))
    .filter((group) => group.items.length > 0);
}

/** The id of the group holding `viewMode`, or null for a page in no group. */
export function groupOfView(viewMode, groups = NAV_GROUPS) {
  const group = groups.find((g) => g.items.some((item) => item.mode === viewMode));
  return group ? group.id : null;
}
