// automation-form: the automation rule form state and the REST payload it
// sends — S3 blocks (a blank secret keeps the stored one), backup and sync
// actions, the payload per rule type, reading a stored rule back into the
// form (and round-tripping it) and the one-line cron action summary.
import { describe, test, expect } from 'vitest';
import {
  EMPTY_RULE,
  EMPTY_BACKUP,
  EMPTY_SYNC,
  buildStorage,
  buildBackup,
  buildSync,
  buildPayload,
  ruleToForm,
  describeCronAction,
  isRunnable,
  describeLastResult,
} from './automation-form';

const MIB = 1024 * 1024;

const storage = {
  endpoint: ' s3.example.com ', region: ' eu-west-1 ', accessKey: ' AK ', secretKey: 's3cr3t ', useTLS: false, secretKeySet: true,
};

describe('buildStorage', () => {
  test.each([
    ['trims fields and sends a typed secret as is', storage,
      { endpoint: 's3.example.com', region: 'eu-west-1', accessKey: 'AK', secretKey: 's3cr3t ', useTLS: false }],
    ['leaves out a blank secret and blank fields', EMPTY_BACKUP.storageConfig, { useTLS: true }],
  ])('%s', (_label, input, want) => {
    expect(buildStorage(input)).toEqual(want);
  });
});

describe('buildBackup', () => {
  test.each([
    ['local default: every collection, no path', EMPTY_BACKUP, { target: 'local', collections: ['*'] }],
    ['local with a directory, keep and a collection list',
      { ...EMPTY_BACKUP, path: ' nightly ', prefix: ' mddb- ', keep: '7', collections: ' a, ,b ' },
      { target: 'local', path: 'nightly', prefix: 'mddb-', keep: 7, collections: ['a', 'b'] }],
    ['blank collection list means all, keep that is not a positive integer is dropped',
      { ...EMPTY_BACKUP, collections: ' , ', keep: '-2' }, { target: 'local', collections: ['*'] }],
    ['fractional keep is dropped', { ...EMPTY_BACKUP, keep: '1.5' }, { target: 'local', collections: ['*'] }],
    ['s3 sends bucket and storage, never the local path',
      { ...EMPTY_BACKUP, target: 's3', path: 'ignored', bucket: ' bk ', storageConfig: storage },
      {
        target: 's3', bucket: 'bk', collections: ['*'],
        storageConfig: { endpoint: 's3.example.com', region: 'eu-west-1', accessKey: 'AK', secretKey: 's3cr3t ', useTLS: false },
      }],
    ['s3 with a blank bucket leaves it out',
      { ...EMPTY_BACKUP, target: 's3' }, { target: 's3', collections: ['*'], storageConfig: { useTLS: true } }],
  ])('%s', (_label, input, want) => {
    expect(buildBackup(input)).toEqual(want);
  });
});

describe('buildSync', () => {
  test.each([
    ['sitemap with limits and prune',
      { ...EMPTY_SYNC, collection: 'docs', url: ' https://x/sitemap.xml ', maxDocuments: '100', maxMiB: '3', prune: true },
      { source: 'sitemap', collection: 'docs', lang: 'en', url: 'https://x/sitemap.xml', maxDocuments: 100, maxBytes: 3 * MIB, prune: true }],
    ['urls split by line, blank lines dropped, bad limits omitted',
      { ...EMPTY_SYNC, source: 'urls', collection: 'docs', lang: ' ', urls: ' https://a \n\n https://b\n', maxDocuments: 'x', maxMiB: '0' },
      { source: 'urls', collection: 'docs', urls: ['https://a', 'https://b'] }],
    ['s3 sends bucket, prefix and storage',
      { ...EMPTY_SYNC, source: 's3', collection: 'docs', bucket: ' bk ', prefix: ' docs/ ', storageConfig: storage },
      {
        source: 's3', collection: 'docs', lang: 'en', bucket: 'bk', prefix: 'docs/',
        storageConfig: { endpoint: 's3.example.com', region: 'eu-west-1', accessKey: 'AK', secretKey: 's3cr3t ', useTLS: false },
      }],
    ['s3 with blank bucket and prefix leaves both out',
      { ...EMPTY_SYNC, source: 's3' }, { source: 's3', lang: 'en', storageConfig: { useTLS: true } }],
  ])('%s', (_label, input, want) => {
    expect(buildSync(input)).toEqual(want);
  });
});

describe('buildPayload', () => {
  test.each([
    ['webhook without headers', { ...EMPTY_RULE, name: 'w', url: 'https://h' },
      { type: 'webhook', name: 'w', enabled: true, url: 'https://h', method: 'POST' }],
    ['webhook with JSON headers', { ...EMPTY_RULE, name: 'w', url: 'u', method: 'PUT', headers: '{"A":"1"}' },
      { type: 'webhook', name: 'w', enabled: true, url: 'u', method: 'PUT', headers: { A: '1' } }],
    ['webhook with broken headers sends an empty object', { ...EMPTY_RULE, name: 'w', headers: '{nope' },
      { type: 'webhook', name: 'w', enabled: true, url: '', method: 'POST', headers: {} }],
    ['trigger, no events falls back to insert+update, sentiment off',
      { ...EMPTY_RULE, type: 'trigger', name: 't', collection: 'c', query: 'q', events: [] },
      { type: 'trigger', name: 't', enabled: true, collection: 'c', searchType: 'fts', query: 'q', threshold: 50, events: ['insert', 'update'], sentimentEnabled: false }],
    ['trigger with undefined events also falls back',
      { ...EMPTY_RULE, type: 'trigger', name: 't', events: undefined },
      { type: 'trigger', name: 't', enabled: true, collection: '', searchType: 'fts', query: '', threshold: 50, events: ['insert', 'update'], sentimentEnabled: false }],
    ['trigger with sentiment but no query has no condition logic',
      { ...EMPTY_RULE, type: 'trigger', name: 't', sentimentEnabled: true, sentimentMin: -0.2, sentimentMax: 0.4, webhookId: 'w1', events: ['delete'] },
      {
        type: 'trigger', name: 't', enabled: true, collection: '', searchType: 'fts', query: '', threshold: 50, events: ['delete'],
        webhookId: 'w1', sentimentEnabled: true, sentimentMin: -0.2, sentimentMax: 0.4,
      }],
    ['cron calling a webhook', { ...EMPTY_RULE, type: 'cron', name: 'c', schedule: '0 0 9 * * *', webhookId: 'w1' },
      { type: 'cron', name: 'c', enabled: true, schedule: '0 0 9 * * *', webhookId: 'w1' }],
    ['cron without a webhook sends no action', { ...EMPTY_RULE, type: 'cron', name: 'c', schedule: 's' },
      { type: 'cron', name: 'c', enabled: true, schedule: 's' }],
    ['cron backup ignores a leftover webhookId', { ...EMPTY_RULE, type: 'cron', name: 'c', schedule: 's', cronAction: 'backup', webhookId: 'w1' },
      { type: 'cron', name: 'c', enabled: true, schedule: 's', backup: { target: 'local', collections: ['*'] } }],
    ['cron sync', { ...EMPTY_RULE, type: 'cron', name: 'c', schedule: 's', cronAction: 'sync', sync: { ...EMPTY_SYNC, collection: 'd', url: 'https://x' } },
      { type: 'cron', name: 'c', enabled: true, schedule: 's', sync: { source: 'sitemap', collection: 'd', lang: 'en', url: 'https://x' } }],
    ['unknown type sends only the common fields', { ...EMPTY_RULE, type: 'other', name: 'o', enabled: false },
      { type: 'other', name: 'o', enabled: false }],
  ])('%s', (_label, form, want) => {
    expect(buildPayload(form)).toEqual(want);
  });
});

describe('ruleToForm', () => {
  test('an empty rule gives the defaults', () => {
    expect(ruleToForm({})).toEqual({ ...EMPTY_RULE });
  });

  test('a stored S3 block: secret blank, secretKeySet kept, missing useTLS means off', () => {
    const form = ruleToForm({
      type: 'cron',
      backup: { target: 's3', bucket: 'bk', storageConfig: { endpoint: 'e', secretKey: '', secretKeySet: true } },
    });
    expect(form.cronAction).toBe('backup');
    expect(form.backup).toEqual({
      target: 's3', path: '', bucket: 'bk', prefix: '', keep: '', collections: '*',
      storageConfig: { endpoint: 'e', region: '', accessKey: '', secretKey: '', useTLS: false, secretKeySet: true },
    });
    expect(form.sync).toBe(EMPTY_SYNC);
  });

  test('a backup without fields defaults to local, all collections, no storage', () => {
    const form = ruleToForm({ type: 'cron', backup: {} });
    expect(form.backup).toEqual({
      target: 'local', path: '', bucket: '', prefix: '', keep: '', collections: '*',
      storageConfig: { ...EMPTY_BACKUP.storageConfig, useTLS: false },
    });
  });

  test('a minimal sync fills blanks; sync wins over backup', () => {
    const form = ruleToForm({ type: 'cron', backup: {}, sync: { source: 'sitemap', collection: 'd', lang: 'en' } });
    expect(form.cronAction).toBe('sync');
    expect(form.sync).toEqual({
      source: 'sitemap', collection: 'd', lang: 'en', url: '', urls: '', bucket: '', prefix: '', prune: false,
      maxDocuments: '', maxMiB: '', storageConfig: { ...EMPTY_SYNC.storageConfig, useTLS: false },
    });
  });

  test('maxBytes reads back as whole MiB and urls as lines', () => {
    const form = ruleToForm({ type: 'cron', sync: { source: 'urls', collection: 'd', lang: 'en', urls: ['a', 'b'], maxBytes: 2.4 * MIB, maxDocuments: 9 } });
    expect(form.sync.urls).toBe('a\nb');
    expect(form.sync.maxMiB).toBe('2');
    expect(form.sync.maxDocuments).toBe('9');
  });

  test('a cron webhook rule keeps webhook as its action', () => {
    expect(ruleToForm({ type: 'cron', schedule: 's', webhookId: 'w1' })).toMatchObject({ cronAction: 'webhook', webhookId: 'w1', schedule: 's' });
  });

  test.each([
    ['webhook', { type: 'webhook', name: 'w', enabled: false, url: 'https://h', method: 'PUT', headers: { A: '1' } }],
    ['trigger', {
      type: 'trigger', name: 't', enabled: true, collection: 'c', searchType: 'vector', query: 'q', threshold: 0,
      events: ['delete'], webhookId: 'w1', sentimentEnabled: true, sentimentMin: -0.5, sentimentMax: 0.5, conditionLogic: 'or',
    }],
    ['cron webhook', { type: 'cron', name: 'c', enabled: true, schedule: '0 0 9 * * *', webhookId: 'w1' }],
    ['cron local backup', {
      type: 'cron', name: 'b', enabled: true, schedule: 's',
      backup: { target: 'local', path: 'nightly', prefix: 'mddb-', keep: 3, collections: ['a', 'b'] },
    }],
    ['cron s3 backup (stored secret not resent)', {
      type: 'cron', name: 'b', enabled: true, schedule: 's',
      backup: { target: 's3', bucket: 'bk', collections: ['*'], storageConfig: { endpoint: 'e', region: 'r', accessKey: 'AK', useTLS: true } },
    }],
    ['cron urls sync', {
      type: 'cron', name: 's', enabled: true, schedule: 's',
      sync: { source: 'urls', collection: 'd', lang: 'en', urls: ['https://a', 'https://b'], prune: true, maxDocuments: 100, maxBytes: 2 * MIB },
    }],
    ['cron s3 sync', {
      type: 'cron', name: 's', enabled: true, schedule: 's',
      sync: { source: 's3', collection: 'd', lang: 'pl', bucket: 'bk', prefix: 'docs/', storageConfig: { endpoint: 'e', useTLS: false } },
    }],
  ])('round trip: %s', (_label, rule) => {
    const read = rule.backup?.storageConfig
      ? { ...rule, backup: { ...rule.backup, storageConfig: { ...rule.backup.storageConfig, secretKey: '', secretKeySet: true } } }
      : rule;
    expect(buildPayload(ruleToForm(read))).toEqual(rule);
  });
});

describe('describeCronAction', () => {
  test.each([
    ['sitemap sync', { sync: { source: 'sitemap', url: 'https://x/sm.xml', collection: 'd' } }, 'sync sitemap https://x/sm.xml → d'],
    ['urls sync with prune', { sync: { source: 'urls', urls: ['a', 'b'], collection: 'd', prune: true } }, 'sync urls 2 URLs → d (prune)'],
    ['urls sync without urls', { sync: { source: 'urls', collection: 'd' } }, 'sync urls 0 URLs → d'],
    ['s3 sync with prefix', { sync: { source: 's3', bucket: 'bk', prefix: 'docs/', collection: 'd' } }, 'sync s3 s3://bk/docs/ → d'],
    ['s3 sync without prefix', { sync: { source: 's3', bucket: 'bk', collection: 'd' } }, 'sync s3 s3://bk/ → d'],
    ['s3 backup with keep', { backup: { target: 's3', bucket: 'bk', keep: 5 } }, 'backup → s3://bk, keep 5'],
    ['s3 backup bucket from storage', { backup: { target: 's3', storageConfig: { bucket: 'sb' } } }, 'backup → s3://sb'],
    ['s3 backup without bucket', { backup: { target: 's3' } }, 'backup → s3://'],
    ['local backup path', { backup: { target: 'local', path: 'nightly' } }, 'backup → nightly'],
    ['local backup default dir', { backup: {} }, 'backup → MDDB_BACKUP_DIR'],
    ['webhook', { webhookId: 'w1' }, 'webhook w1'],
    ['nothing', {}, ''],
  ])('%s', (_label, rule, want) => {
    expect(describeCronAction(rule)).toBe(want);
  });
});

describe('isRunnable', () => {
  test.each([
    ['a sync cron', { type: 'cron', sync: {} }, true],
    ['a backup cron', { type: 'cron', backup: {} }, true],
    ['a webhook cron', { type: 'cron', webhookId: 'w' }, false],
    ['a trigger', { type: 'trigger' }, false],
  ])('%s', (_label, rule, want) => {
    expect(isRunnable(rule)).toBe(want);
  });
});

describe('describeLastResult', () => {
  test.each([
    ['never ran', { sync: {} }, ''],
    ['a sync with every count', { sync: {}, lastResult: { status: 'success', added: 1, updated: 2, unchanged: 3, deleted: 4, failed: 0 } },
      'success · +1 ~2 =3 −4 ✕0'],
    ['a sync with no counts sent', { sync: {}, lastResult: { status: 'skipped', error: 'still going' } },
      'skipped · +0 ~0 =0 −0 ✕0 · still going'],
    ['a backup', { backup: {}, lastResult: { status: 'error', error: 'disk full' } }, 'error · disk full'],
  ])('%s', (_label, rule, want) => {
    expect(describeLastResult(rule)).toBe(want);
  });
});
