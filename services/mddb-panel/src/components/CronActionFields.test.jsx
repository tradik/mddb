// @vitest-environment jsdom
// CronActionFields: the cron rule's schedule and its one action — the action
// select (webhook, backup, sync), the backup and sync sub-forms, the S3 fields
// and the hint that says whether a secret key is already stored.
import { describe, test, expect, vi, afterEach } from 'vitest';
import { useState } from 'react';
import { render, screen, cleanup, fireEvent } from '@testing-library/react';
import CronActionFields from './CronActionFields';
import { EMPTY_RULE, EMPTY_BACKUP, EMPTY_SYNC } from '../lib/automation-form';

const webhooks = [
  { id: 'w1', name: 'Slack', url: 'https://hooks/slack' },
  { id: 'w2', name: 'Ping', method: 'GET', url: 'https://ping' },
];
const collections = [{ name: 'docs' }, { name: 'news' }];

/** Renders the fields over real form state; `seen` gets every new state. */
function renderFields(initial, seen = vi.fn()) {
  function Harness() {
    const [form, setForm] = useState({ ...EMPTY_RULE, type: 'cron', ...initial });
    const update = (next) => {
      seen(next);
      setForm(next);
    };
    return <CronActionFields form={form} setForm={update} webhooks={webhooks} collections={collections} />;
  }
  render(<Harness />);
  return seen;
}

/** The state the last change produced. */
const last = (seen) => seen.mock.calls.at(-1)[0];

const change = (label, value) => fireEvent.change(screen.getByLabelText(label), { target: { value } });

afterEach(cleanup);

describe('CronActionFields', () => {
  test('webhook action: schedule and a webhook picked from the list', () => {
    const seen = renderFields({});
    expect(screen.getByRole('option', { name: 'Slack (POST https://hooks/slack)' })).toBeTruthy();
    expect(screen.getByRole('option', { name: 'Ping (GET https://ping)' })).toBeTruthy();
    expect(screen.getByText('6 fields: sec min hour day month weekday')).toBeTruthy();
    const schedule = screen.getByLabelText(/^Schedule/);
    expect(schedule.className).toContain('font-mono');

    fireEvent.change(schedule, { target: { value: '0 0 9 * * *' } });
    change('Webhook', 'w2');
    expect(last(seen)).toMatchObject({ schedule: '0 0 9 * * *', webhookId: 'w2', cronAction: 'webhook' });
    expect(screen.queryByLabelText('Backup target')).toBeNull();
    expect(screen.queryByLabelText('Source')).toBeNull();
  });

  test.each([
    ['backup', 'Backup target', 'Webhook'],
    ['sync', 'Source', 'Webhook'],
    ['webhook', 'Webhook', 'Source'],
  ])('choosing the %s action shows its fields only', (action, shown, hidden) => {
    const seen = renderFields({ cronAction: action === 'webhook' ? 'sync' : 'webhook' });
    change('Action', action);
    expect(last(seen).cronAction).toBe(action);
    expect(screen.getByLabelText(shown)).toBeTruthy();
    expect(screen.queryByLabelText(hidden)).toBeNull();
  });

  test('local backup: directory, prefix, keep and collections', () => {
    const seen = renderFields({ cronAction: 'backup' });
    expect(screen.queryByLabelText('Bucket')).toBeNull();
    expect(screen.queryByLabelText(/^S3 endpoint/)).toBeNull();
    expect(screen.getByText('Older backups are deleted only after a successful run.')).toBeTruthy();

    change('Directory (empty = MDDB_BACKUP_DIR)', 'nightly');
    change('Name prefix', 'mddb-');
    change(/^Keep newest/, '7');
    change('Collections (comma-separated, * = all)', 'a, b');
    expect(last(seen).backup).toEqual({
      ...EMPTY_BACKUP, path: 'nightly', prefix: 'mddb-', keep: '7', collections: 'a, b',
    });
  });

  test('s3 backup: bucket and storage fields, no secret stored yet', () => {
    const seen = renderFields({ cronAction: 'backup' });
    change('Backup target', 's3');
    expect(screen.queryByLabelText('Directory (empty = MDDB_BACKUP_DIR)')).toBeNull();
    expect(screen.getByText('No secret stored yet.')).toBeTruthy();
    const secret = screen.getByLabelText(/^Secret key/);
    expect(secret.type).toBe('password');
    expect(secret.placeholder).toBe('');

    change('Bucket', 'bk');
    change('S3 endpoint', 's3.example.com');
    change('Region', 'eu-west-1');
    change('Access key', 'AK');
    fireEvent.change(secret, { target: { value: 'sk' } });
    fireEvent.click(screen.getByLabelText('Use TLS (HTTPS)'));
    expect(last(seen).backup).toEqual({
      ...EMPTY_BACKUP,
      target: 's3',
      bucket: 'bk',
      storageConfig: { endpoint: 's3.example.com', region: 'eu-west-1', accessKey: 'AK', secretKey: 'sk', useTLS: false, secretKeySet: false },
    });
  });

  test('a stored secret says leave blank to keep it', () => {
    renderFields({
      cronAction: 'backup',
      backup: { ...EMPTY_BACKUP, target: 's3', storageConfig: { ...EMPTY_BACKUP.storageConfig, secretKeySet: true } },
    });
    expect(screen.getByText('A secret is stored. Leave blank to keep it.')).toBeTruthy();
    expect(screen.getByLabelText(/^Secret key/).placeholder).toBe('Stored — leave blank to keep');
  });

  test('sitemap sync: collection, language, URL, limits and prune', () => {
    const seen = renderFields({ cronAction: 'sync' });
    expect(screen.getByRole('option', { name: 'news' })).toBeTruthy();
    expect(screen.queryByLabelText('URLs (one per line)')).toBeNull();
    expect(screen.queryByLabelText('Prefix')).toBeNull();

    change('Into collection', 'docs');
    change('Language', 'pl');
    change('Sitemap URL', 'https://x/sitemap.xml');
    change('Max documents per run', '100');
    change('Max MiB per document', '5');
    fireEvent.click(screen.getByLabelText(/^Prune/));
    expect(last(seen).sync).toEqual({
      ...EMPTY_SYNC, collection: 'docs', lang: 'pl', url: 'https://x/sitemap.xml', maxDocuments: '100', maxMiB: '5', prune: true,
    });
  });

  test('urls sync: one URL per line in a textarea', () => {
    const seen = renderFields({ cronAction: 'sync' });
    change('Source', 'urls');
    expect(screen.queryByLabelText('Sitemap URL')).toBeNull();
    const urls = screen.getByLabelText('URLs (one per line)');
    expect(urls.tagName).toBe('TEXTAREA');
    fireEvent.change(urls, { target: { value: 'https://a\nhttps://b' } });
    expect(last(seen).sync).toMatchObject({ source: 'urls', urls: 'https://a\nhttps://b' });
  });

  test('s3 sync: bucket, prefix and the S3 store', () => {
    const seen = renderFields({ cronAction: 'sync' });
    change('Source', 's3');
    change('Bucket', 'bk');
    change('Prefix', 'docs/');
    change('S3 endpoint', 'minio:9000');
    expect(screen.getByText('No secret stored yet.')).toBeTruthy();
    expect(last(seen).sync).toMatchObject({
      source: 's3', bucket: 'bk', prefix: 'docs/', storageConfig: { ...EMPTY_SYNC.storageConfig, endpoint: 'minio:9000' },
    });
  });
});
