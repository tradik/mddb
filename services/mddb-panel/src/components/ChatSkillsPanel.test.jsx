// @vitest-environment jsdom
// ChatSkillsPanel (#404): the Skills tab — list, editor, import with the
// whole text shown before saving, update from source with a diff, used by,
// earlier versions and delete.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, within, waitFor } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({
  default: {
    listChatSkills: vi.fn(),
    getChatSkill: vi.fn(),
    putChatSkill: vi.fn(),
    deleteChatSkill: vi.fn(),
    importChatSkill: vi.fn(),
    updateChatSkill: vi.fn(),
  },
}));

const { default: mddbClient } = await import('../lib/mddb-client');
const { default: ChatSkillsPanel } = await import('./ChatSkillsPanel');
const { default: ChatSkillHistory } = await import('./ChatSkillHistory');

const source = { url: 'https://github.com/acme/skills/blob/main/SKILL.md', commit: '1111111aaaa', fetchedAt: '2026-11-11T10:00:00Z' };
const tone = {
  name: 'tone', description: 'Friendly', content: '# Tone\nBe kind.', source, version: 2, bytes: 15,
  usedBy: ['support'], history: [{ version: 1, bytes: 8, source }],
};
const plain = { name: 'plain', content: 'Plain.', version: 1, bytes: 6, usedBy: [] };

const field = (label) => screen.getByLabelText(label);
const type = (label, value) => fireEvent.change(field(label), { target: { value } });
const click = (name) => fireEvent.click(screen.getByRole('button', { name }));

async function renderPanel() {
  render(<ChatSkillsPanel />);
  await screen.findByText('version 7');
}

beforeEach(() => {
  vi.mocked(mddbClient.listChatSkills).mockResolvedValue({ skills: [{ ...tone, content: undefined }, plain], version: 7 });
  vi.mocked(mddbClient.getChatSkill).mockImplementation(async (name, version) => {
    if (version === 1) return { ...tone, version: 1, content: 'Be kind.', history: undefined };
    return name === 'plain' ? plain : tone;
  });
  vi.mocked(mddbClient.putChatSkill).mockResolvedValue({ version: 3 });
  vi.mocked(mddbClient.deleteChatSkill).mockResolvedValue({ deleted: true });
});
afterEach(cleanup);

describe('the list', () => {
  test('shows version, size, source and use of each skill', async () => {
    await renderPanel();
    const nav = within(screen.getByRole('navigation', { name: 'Skills' }));
    expect(nav.getByRole('button', { name: /tone/ }).textContent).toContain('15 B · github.com/acme/skills/blob/main/SKILL.md @ 1111111 · used by 1 scenario');
    expect(nav.getByRole('button', { name: /plain/ }).textContent).toContain('pasted · used by 0 scenarios');
    expect(screen.getByText('Pick a skill to edit it, write a new one, or import one.')).toBeTruthy();
  });

  test('an empty list, and a refused one', async () => {
    vi.mocked(mddbClient.listChatSkills).mockResolvedValueOnce({});
    render(<ChatSkillsPanel />);
    expect(await screen.findByText('No skills yet.')).toBeTruthy();
    cleanup();
    vi.mocked(mddbClient.listChatSkills).mockRejectedValueOnce(new Error('HTTP 403: admin access required'));
    render(<ChatSkillsPanel />);
    expect((await screen.findByRole('alert')).textContent).toBe('HTTP 403: admin access required');
  });
});

describe('writing a skill', () => {
  test('a pasted SKILL.md is split by the server, then saved and reopened', async () => {
    vi.mocked(mddbClient.importChatSkill).mockResolvedValue({ name: 'brief', description: 'Short answers', content: 'Be brief.' });
    await renderPanel();
    click('New skill');
    expect(screen.getByRole('button', { name: 'Read front matter' }).disabled).toBe(true);
    type('Instructions (Markdown, or a whole SKILL.md)', '---\nname: brief\n---\nBe brief.');
    click('Read front matter');
    await waitFor(() => expect(field('Name').value).toBe('brief'));
    expect(mddbClient.importChatSkill).toHaveBeenCalledWith({ content: '---\nname: brief\n---\nBe brief.' });
    expect(field('Description').value).toBe('Short answers');
    expect(field('Instructions (Markdown, or a whole SKILL.md)').value).toBe('Be brief.');
    click('Save');
    expect((await screen.findByRole('status')).textContent).toContain('Saved brief, version 3.');
    expect(mddbClient.putChatSkill).toHaveBeenCalledWith('brief', { description: 'Short answers', content: 'Be brief.' });
  });

  test('front matter without a name keeps the typed one; a parse error is shown', async () => {
    vi.mocked(mddbClient.importChatSkill).mockResolvedValueOnce({ content: 'Body.' });
    await renderPanel();
    click('New skill');
    type('Name', 'mine');
    type('Description', 'Typed');
    type('Instructions (Markdown, or a whole SKILL.md)', 'Body.');
    click('Read front matter');
    await waitFor(() => expect(mddbClient.importChatSkill).toHaveBeenCalled());
    expect(field('Name').value).toBe('mine');
    expect(field('Description').value).toBe('Typed');
    vi.mocked(mddbClient.importChatSkill).mockRejectedValueOnce(new Error('HTTP 400: content is too long'));
    click('Read front matter');
    expect((await screen.findByRole('alert')).textContent).toBe('HTTP 400: content is too long');
  });

  test('an incomplete form is refused here; a refused save shows the server message', async () => {
    await renderPanel();
    click('New skill');
    click('Save');
    expect(screen.getByRole('alert').textContent).toBe('Give the skill a name.');
    type('Name', 'tone2');
    type('Instructions (Markdown, or a whole SKILL.md)', 'x');
    vi.mocked(mddbClient.putChatSkill).mockRejectedValueOnce(new Error('HTTP 400: content is over the limit'));
    click('Save');
    expect((await screen.findByRole('alert')).textContent).toBe('HTTP 400: content is over the limit');
  });

  test('editing an imported skill detaches it from its source', async () => {
    await renderPanel();
    fireEvent.click(screen.getByRole('button', { name: /plain/ }));
    await screen.findByText('Source: pasted.');
    fireEvent.click(screen.getByRole('button', { name: /tone/ }));
    expect(await screen.findByText(/Editing the instructions detaches/)).toBeTruthy();
    expect(field('Name').disabled).toBe(true);
    type('Instructions (Markdown, or a whole SKILL.md)', 'Be kinder.');
    expect(screen.getByText('Source: pasted.')).toBeTruthy();
    click('Save');
    await screen.findByRole('status');
    expect(mddbClient.putChatSkill).toHaveBeenCalledWith('tone', { description: 'Friendly', content: 'Be kinder.' });
  });

  test('a skill that cannot be read shows the error', async () => {
    vi.mocked(mddbClient.getChatSkill).mockRejectedValueOnce(new Error('HTTP 404: chat skill not found'));
    await renderPanel();
    fireEvent.click(screen.getByRole('button', { name: /plain/ }));
    expect((await screen.findByRole('alert')).textContent).toBe('HTTP 404: chat skill not found');
  });
});

describe('deleting', () => {
  test('asks first, and shows the scenarios a refused delete names', async () => {
    const ask = vi.fn(() => false);
    vi.stubGlobal('confirm', ask);
    await renderPanel();
    fireEvent.click(screen.getByRole('button', { name: /tone/ }));
    await screen.findByText('Used by');
    expect(screen.getByText('support')).toBeTruthy();
    click('Delete');
    expect(ask.mock.calls[0][0]).toBe('Delete the skill "tone" and its earlier versions?');
    expect(mddbClient.deleteChatSkill).not.toHaveBeenCalled();
    ask.mockReturnValue(true);
    vi.mocked(mddbClient.deleteChatSkill).mockRejectedValueOnce(new Error('HTTP 409: chat skill tone is in use by: support'));
    click('Delete');
    expect((await screen.findByRole('alert')).textContent).toBe('HTTP 409: chat skill tone is in use by: support');
    click('Delete');
    expect((await screen.findByRole('status')).textContent).toBe('Deleted tone.');
    expect(screen.getByText('Pick a skill to edit it, write a new one, or import one.')).toBeTruthy();
  });
});

describe('history', () => {
  test('compares an earlier version with the current one', async () => {
    await renderPanel();
    fireEvent.click(screen.getByRole('button', { name: /plain/ }));
    expect(await screen.findByText('This is the first version.')).toBeTruthy();
    expect(screen.getByText('No scenario attaches this skill.')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: /tone/ }));
    click(await screen.findByText('Compare v1 with v2').then((b) => b.textContent));
    expect(await screen.findByText('v1 → v2: 1 line added, 0 removed')).toBeTruthy();
    expect(mddbClient.getChatSkill).toHaveBeenLastCalledWith('tone', 1);
    vi.mocked(mddbClient.getChatSkill).mockRejectedValueOnce(new Error('HTTP 404: version 1 is not kept'));
    click('Compare v1 with v2');
    expect((await screen.findByRole('alert')).textContent).toBe('HTTP 404: version 1 is not kept');
  });

  test('a skill without usedBy or history reads as unused and first', () => {
    render(<ChatSkillHistory skill={{ name: 'x', version: 1 }} onError={vi.fn()} />);
    expect(screen.getByText('No scenario attaches this skill.')).toBeTruthy();
    expect(screen.getByText('This is the first version.')).toBeTruthy();
  });
});
