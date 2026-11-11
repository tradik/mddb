// @vitest-environment jsdom
// #404: importing a skill from a URL (the whole text before saving) and
// updating one from its source (a diff before confirming), through the
// Skills tab, and LineDiff on its own.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, waitFor } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({
  default: {
    listChatSkills: vi.fn(),
    getChatSkill: vi.fn(),
    putChatSkill: vi.fn(),
    importChatSkill: vi.fn(),
    updateChatSkill: vi.fn(),
  },
}));

const { default: mddbClient } = await import('../lib/mddb-client');
const { default: ChatSkillsPanel } = await import('./ChatSkillsPanel');
const { default: LineDiff } = await import('./LineDiff');

const source = { url: 'https://github.com/acme/skills/blob/main/SKILL.md', commit: '1111111aaaa', fetchedAt: '2026-11-11T10:00:00Z' };
const moved = { ...source, commit: '2222222bbbb', fetchedAt: '2026-11-12T10:00:00Z' };
const tone = { name: 'tone', description: 'Friendly', content: '# Tone\nBe kind.', source, version: 2, usedBy: [], history: [] };
const raw = '---\nname: tone\ndescription: Friendly\n---\n# Tone\nBe kind.';

const click = (name) => fireEvent.click(screen.getByRole('button', { name }));

beforeEach(() => {
  vi.mocked(mddbClient.listChatSkills).mockResolvedValue({ skills: [tone], version: 3 });
  vi.mocked(mddbClient.getChatSkill).mockResolvedValue(tone);
  vi.mocked(mddbClient.putChatSkill).mockResolvedValue({ version: 1 });
});
afterEach(cleanup);

async function renderPanel() {
  render(<ChatSkillsPanel />);
  await screen.findByText('version 3');
}

describe('import from a URL', () => {
  test('shows the whole fetched text, then saves it with its source', async () => {
    vi.mocked(mddbClient.importChatSkill).mockResolvedValue({ name: 'tone', description: 'Friendly', content: '# Tone\nBe kind.', raw, source, bytes: 15, exists: true });
    await renderPanel();
    click('Import from URL');
    fireEvent.change(screen.getByLabelText('SKILL.md URL (https)'), { target: { value: ` ${source.url} ` } });
    click('Fetch preview');
    const text = await screen.findByLabelText('The whole fetched text');
    expect(text.textContent).toBe(raw);
    expect(mddbClient.importChatSkill).toHaveBeenCalledWith({ url: source.url });
    expect(screen.getByText(/github.com\/acme\/skills\/blob\/main\/SKILL.md @ 1111111 · instructions 15 B · Friendly/)).toBeTruthy();
    expect(screen.getByRole('status').textContent).toContain('saving under that name stores a new version');
    fireEvent.change(screen.getByLabelText('Save as'), { target: { value: 'tone2' } });
    click('Save');
    await waitFor(() => expect(mddbClient.putChatSkill).toHaveBeenCalledWith('tone2', { description: 'Friendly', content: '# Tone\nBe kind.', source }));
  });

  test('a preview without a name or description, and a refused fetch', async () => {
    vi.mocked(mddbClient.importChatSkill).mockResolvedValueOnce({ content: 'x', raw: 'x', bytes: 1, exists: false });
    await renderPanel();
    click('Import from URL');
    fireEvent.change(screen.getByLabelText('SKILL.md URL (https)'), { target: { value: 'https://example.com/s.md' } });
    click('Fetch preview');
    await screen.findByLabelText('The whole fetched text');
    expect(screen.getByLabelText('Save as').value).toBe('');
    expect(screen.queryByRole('status')).toBeNull();
    click('Save');
    expect(screen.getByRole('alert').textContent).toBe('Give the skill a name.');
    expect(mddbClient.putChatSkill).not.toHaveBeenCalled();
    vi.mocked(mddbClient.importChatSkill).mockRejectedValueOnce(new Error('HTTP 400: skills are imported over https only'));
    click('Fetch preview');
    expect((await screen.findByRole('alert')).textContent).toBe('HTTP 400: skills are imported over https only');
  });
});

describe('update from source', () => {
  async function openTone() {
    await renderPanel();
    fireEvent.click(screen.getByRole('button', { name: /tone/ }));
    await screen.findByText(/It is fetched again only when you ask/);
  }

  test('an unchanged source says so', async () => {
    vi.mocked(mddbClient.updateChatSkill).mockResolvedValue({ changed: false, saved: false, preview: { source } });
    await openTone();
    click('Update from source');
    expect((await screen.findByRole('status')).textContent).toBe('The source holds the stored text: nothing to update.');
    expect(mddbClient.updateChatSkill).toHaveBeenCalledWith('tone', {});
  });

  test('a moved source shows a diff, and a confirm names the text shown', async () => {
    const preview = { description: 'Warm', content: '# Tone\nBe kind and brief.', source: moved, sha256: 'abc' };
    vi.mocked(mddbClient.updateChatSkill).mockResolvedValueOnce({ changed: true, saved: false, preview });
    await openTone();
    click('Update from source');
    expect(await screen.findByText('Instructions: 1 line added, 1 removed')).toBeTruthy();
    expect(screen.getByText('Description: 1 line added, 1 removed')).toBeTruthy();
    expect(screen.getByText(/Now at github.com\/acme\/skills\/blob\/main\/SKILL.md @ 2222222/)).toBeTruthy();
    expect(screen.getAllByText('removed:', { exact: false }).length).toBeGreaterThan(0);
    vi.mocked(mddbClient.updateChatSkill).mockResolvedValueOnce({ saved: true, skill: { version: 3 }, preview });
    click('Save as version 3');
    expect((await screen.findByRole('status')).textContent).toBe('Updated tone to version 3 from github.com/acme/skills/blob/main/SKILL.md @ 2222222.');
    expect(mddbClient.updateChatSkill).toHaveBeenLastCalledWith('tone', { confirm: true, sha256: 'abc' });
  });

  test('the same description is not diffed; a refused update shows the server message', async () => {
    vi.mocked(mddbClient.updateChatSkill).mockResolvedValueOnce({ changed: true, saved: false, preview: { ...tone, source: moved, sha256: 'x' } });
    await openTone();
    click('Update from source');
    expect(await screen.findByText('Instructions: 0 lines added, 0 removed')).toBeTruthy();
    expect(screen.queryByText(/^Description:/)).toBeNull();
    vi.mocked(mddbClient.updateChatSkill).mockRejectedValueOnce(new Error('HTTP 409: the source changed since the preview'));
    click('Save as version 3');
    expect((await screen.findByRole('alert')).textContent).toBe('HTTP 409: the source changed since the preview');
  });
});

describe('LineDiff', () => {
  test('marks each changed line with a sign and a word, and says when nothing changed', () => {
    render(<LineDiff label="Text" before={'a\nb'} after={'a\nc\nd'} />);
    expect(screen.getByText('Text: 2 lines added, 1 removed')).toBeTruthy();
    expect(screen.getAllByText('added:', { exact: false })).toHaveLength(2);
    cleanup();
    render(<LineDiff label="Text" before="a" after="a" />);
    expect(screen.getByText('No change.')).toBeTruthy();
  });
});
