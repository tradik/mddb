// mddb-client: system, MCP, users and groups, embedding models, automation,
// collection config, curation, temporal, spelling, audit, encryption and
// retrieval gain. Each row asserts the URL, query string, method and body.
// Names typed by an operator are URL-encoded, so a row with "a b/c" proves
// they cannot change the path or add query parameters.
import { describe, test, vi } from 'vitest';
import { expectEndpoint } from '../../test/fetch-mock.js';

vi.mock('./auth', () => ({ authManager: { getToken: () => null } }));

const { default: client } = await import('./mddb-client');

const POST = 'POST';
const GET = 'GET';
const PUT = 'PUT';
const DELETE = 'DELETE';
const odd = 'a b/c';
const oddEnc = 'a%20b%2Fc';

describe('system and MCP', () => {
  test.each([
    ['getSystemInfo', (c) => c.getSystemInfo(), '/v1/system/info', GET],
    ['getConfig', (c) => c.getConfig(), '/v1/config', GET],
    ['getEndpoints', (c) => c.getEndpoints(), '/v1/endpoints', GET],
    ['getHealth', (c) => c.getHealth(), '/v1/health', GET],
    ['getComplianceStatus', (c) => c.getComplianceStatus(), '/v1/compliance-status', GET],
    ['getMCPConfig', (c) => c.getMCPConfig(), '/v1/mcp/config', GET],
    ['listMCPAPIKeys', (c) => c.listMCPAPIKeys(), '/v1/mcp/keys', GET],
    ['createMCPAPIKey default expiry', (c) => c.createMCPAPIKey('ci'), '/v1/mcp/keys', POST, { name: 'ci', expiresAt: 0 }],
    ['deleteMCPAPIKey', (c) => c.deleteMCPAPIKey('k'), '/v1/mcp/keys', DELETE, { key: 'k' }],
    ['disableMCPAPIKey', (c) => c.disableMCPAPIKey('k'), '/v1/mcp/keys/disable', POST, { key: 'k' }],
    ['getGain defaults to 30 days', (c) => c.getGain(), '/v1/gain?window=30d', GET],
    ['getGain custom window', (c) => c.getGain({ window: '7d' }), '/v1/gain?window=7d', GET],
  ])('%s', (_label, ...row) => expectEndpoint(client, ...row));
});

describe('users and groups', () => {
  test.each([
    ['listUsers', (c) => c.listUsers(), '/v1/auth/users', GET],
    ['createUser defaults admin', (c) => c.createUser({ username: 'u', password: 'p' }), '/v1/auth/register', POST, { username: 'u', password: 'p', admin: false }],
    ['deleteUser encodes', (c) => c.deleteUser(odd), `/v1/auth/users/${oddEnc}`, DELETE],
    ['setUserPermission', (c) => c.setUserPermission({ username: 'u', collection: 'c', read: true, write: false, admin: false }), '/v1/auth/permissions', POST, { username: 'u', collection: 'c', read: true, write: false, admin: false }],
    ['getUserPermissions encodes', (c) => c.getUserPermissions('a&admin=1'), '/v1/auth/permissions?username=a%26admin%3D1', GET],
    ['createGroup defaults members', (c) => c.createGroup({ name: 'g', description: 'd' }), '/v1/auth/groups', POST, { name: 'g', description: 'd', members: [] }],
    ['listGroups', (c) => c.listGroups(), '/v1/auth/groups', GET],
    ['getGroup encodes', (c) => c.getGroup(odd), `/v1/auth/groups/${oddEnc}`, GET],
    ['updateGroup', (c) => c.updateGroup(odd, { description: 'd', members: ['u'] }), `/v1/auth/groups/${oddEnc}`, PUT, { description: 'd', members: ['u'] }],
    ['deleteGroup', (c) => c.deleteGroup(odd), `/v1/auth/groups/${oddEnc}`, DELETE],
    ['setGroupPermission', (c) => c.setGroupPermission({ groupName: 'g', collection: 'c', read: true, write: true, admin: false }), '/v1/auth/group-permissions', POST, { groupName: 'g', collection: 'c', read: true, write: true, admin: false }],
    ['getGroupPermissions', (c) => c.getGroupPermissions(odd), `/v1/auth/group-permissions?group=${oddEnc}`, GET],
  ])('%s', (_label, ...row) => expectEndpoint(client, ...row));
});

describe('embedding models', () => {
  const cfg = { name: 'n', provider: 'openai', model: 'm', dimensions: 3, apiKey: 'k', apiUrl: 'u', isDefault: true };
  test.each([
    ['listEmbeddingConfigs', (c) => c.listEmbeddingConfigs(), '/v1/embedding-configs', GET],
    ['getEmbeddingConfig', (c) => c.getEmbeddingConfig(odd), `/v1/embedding-configs/${oddEnc}`, GET],
    ['createEmbeddingConfig', (c) => c.createEmbeddingConfig({ id: 'e', ...cfg }), '/v1/embedding-configs', POST, { id: 'e', ...cfg }],
    ['updateEmbeddingConfig', (c) => c.updateEmbeddingConfig(odd, cfg), `/v1/embedding-configs/${oddEnc}`, PUT, cfg],
    ['deleteEmbeddingConfig', (c) => c.deleteEmbeddingConfig(odd), `/v1/embedding-configs/${oddEnc}`, DELETE],
    ['setDefaultEmbeddingConfig', (c) => c.setDefaultEmbeddingConfig('e'), '/v1/embedding-configs/set-default', POST, { id: 'e' }],
  ])('%s', (_label, ...row) => expectEndpoint(client, ...row));
});

describe('automation', () => {
  const rule = { name: 'r', type: 'trigger' };
  test.each([
    ['listAutomation all', (c) => c.listAutomation(), '/v1/automation', GET],
    ['listAutomation by type', (c) => c.listAutomation('a&b'), '/v1/automation?type=a%26b', GET],
    ['createAutomation', (c) => c.createAutomation(rule), '/v1/automation', POST, rule],
    ['getAutomation', (c) => c.getAutomation(odd), `/v1/automation/${oddEnc}`, GET],
    ['updateAutomation', (c) => c.updateAutomation('r1', rule), '/v1/automation/r1', PUT, rule],
    ['deleteAutomation', (c) => c.deleteAutomation('r1'), '/v1/automation/r1', DELETE],
    ['testAutomation', (c) => c.testAutomation(odd), `/v1/automation/${oddEnc}/test`, POST],
    ['listAutomationLogs defaults', (c) => c.listAutomationLogs(), '/v1/automation-logs?limit=50', GET],
    ['listAutomationLogs every filter', (c) => c.listAutomationLogs({ limit: 0, cursor: 'x', ruleId: 'r', status: 'ok' }), '/v1/automation-logs?cursor=x&ruleId=r&status=ok', GET],
  ])('%s', (_label, ...row) => expectEndpoint(client, ...row));
});

describe('collection config and curation', () => {
  test.each([
    ['getCollectionConfig', (c) => c.getCollectionConfig(odd), `/v1/collection-config?collection=${oddEnc}`, GET],
    ['setCollectionConfig', (c) => c.setCollectionConfig({ collection: 'c', ttl: 1 }), '/v1/collection-config', PUT, { collection: 'c', ttl: 1 }],
    ['deleteCollectionConfig', (c) => c.deleteCollectionConfig('c'), '/v1/collection-config?collection=c', DELETE],
    ['listCollectionConfigs', (c) => c.listCollectionConfigs(), '/v1/collection-configs', GET],
    ['listCurationRules all', (c) => c.listCurationRules(), '/v1/curation', GET],
    ['listCurationRules by collection', (c) => c.listCurationRules(odd), `/v1/curation?collection=${oddEnc}`, GET],
    ['getCurationRule', (c) => c.getCurationRule('r 1'), '/v1/curation?id=r%201', GET],
    ['createCurationRule', (c) => c.createCurationRule({ query: 'q' }), '/v1/curation', POST, { query: 'q' }],
    ['updateCurationRule', (c) => c.updateCurationRule({ id: 'r', query: 'q' }), '/v1/curation', PUT, { id: 'r', query: 'q' }],
    ['deleteCurationRule', (c) => c.deleteCurationRule('r'), '/v1/curation?id=r', DELETE],
  ])('%s', (_label, ...row) => expectEndpoint(client, ...row));
});

describe('temporal and spelling', () => {
  test.each([
    ['temporalQuery', (c) => c.temporalQuery({ collection: 'c', eventType: 'view', limit: 5 }), '/v1/temporal/query', POST, { collection: 'c', eventType: 'view', limit: 5 }],
    ['temporalHot', (c) => c.temporalHot({ collection: 'c', topN: 3, since: 1 }), '/v1/temporal/hot', POST, { collection: 'c', topN: 3, since: 1 }],
    ['temporalHistogram', (c) => c.temporalHistogram({ collection: 'c', interval: 'day' }), '/v1/temporal/histogram', POST, { collection: 'c', interval: 'day' }],
    ['spellSuggest', (c) => c.spellSuggest({ collection: 'c', text: 't', lang: 'en', maxSuggestions: 2 }), '/v1/spell-suggest', POST, { collection: 'c', text: 't', lang: 'en', maxSuggestions: 2 }],
    ['spellCleanup', (c) => c.spellCleanup({ collection: 'c', text: 't', lang: 'en' }), '/v1/spell-cleanup', POST, { collection: 'c', text: 't', lang: 'en' }],
    ['spellDictionary GET global', (c) => c.spellDictionary('GET', { lang: 'en' }), '/v1/spell-dictionary?lang=en', GET],
    ['spellDictionary GET per collection', (c) => c.spellDictionary('GET', { lang: 'en', collection: 'c' }), '/v1/spell-dictionary?lang=en&collection=c', GET],
    ['spellDictionary POST', (c) => c.spellDictionary('POST', { lang: 'en', words: ['w'], frequency: 2 }), '/v1/spell-dictionary', POST, { lang: 'en', words: ['w'], frequency: 2 }],
    ['spellDictionary DELETE', (c) => c.spellDictionary('DELETE', { collection: 'c', lang: 'en', words: ['w'] }), '/v1/spell-dictionary', DELETE, { collection: 'c', lang: 'en', words: ['w'] }],
  ])('%s', (_label, ...row) => expectEndpoint(client, ...row));
});

describe('audit and encryption', () => {
  test.each([
    ['listAuditEvents without filters', (c) => c.listAuditEvents(), '/v1/audit', GET],
    ['listAuditEvents drops empty filters', (c) => c.listAuditEvents({ actor: 'admin', action: '', result: null, from: undefined, limit: 10 }), '/v1/audit?actor=admin&limit=10', GET],
    ['listAuditExporters', (c) => c.listAuditExporters(), '/v1/audit/exporters', GET],
    ['getEncryptionStatus', (c) => c.getEncryptionStatus(), '/v1/encryption/status', GET],
    ['rotateEncryption all collections', (c) => c.rotateEncryption(), '/v1/encryption/rotate', POST, { collection: '' }],
    ['rotateEncryption one collection', (c) => c.rotateEncryption({ collection: 'c' }), '/v1/encryption/rotate', POST, { collection: 'c' }],
    ['listRotationJobs', (c) => c.listRotationJobs(), '/v1/encryption/jobs', GET],
    ['getRotationJob', (c) => c.getRotationJob(odd), `/v1/encryption/jobs/${oddEnc}`, GET],
  ])('%s', (_label, ...row) => expectEndpoint(client, ...row));
});
