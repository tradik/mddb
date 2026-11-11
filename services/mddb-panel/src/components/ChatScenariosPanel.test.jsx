// @vitest-environment jsdom
// ChatScenariosPanel (#403): the scenario list, the editor with every field
// (collections, style, model, extra body, limits), saving and deleting, the
// composed-prompt preview and the "Try it" placeholder.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, within, waitFor } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({
  default: {
    listChatScenarios: vi.fn(),
    putChatScenario: vi.fn(),
    deleteChatScenario: vi.fn(),
    getChatScenarioPrompt: vi.fn(),
  },
}));

const { default: mddbClient } = await import('../lib/mddb-client');
const { useStore } = await import('../lib/store');
const { default: ChatScenariosPanel } = await import('./ChatScenariosPanel');

const initialStore = useStore.getState();

const support = {
  name: 'support',
  description: 'Support desk',
  enabled: true,
  operatorPrompt: 'You are Ada.',
  guardrails: 'No legal advice.',
  offTopic: { mode: 'refuse' },
  allowedCollections: ['docs', 'kb-alias'],
  style: { emoji: 'off', verbosity: 'normal', markdown: true },
  llm: { model: 'gpt-x', temperature: 0.2, extraBody: { reasoning_effort: 'low' } },
  rateLimit: {},
};
const drafts = { name: 'drafts', enabled: false, offTopic: {}, style: {}, llm: {}, rateLimit: {} };

/** Renders the panel over `scenarios` and waits for the list. */
async function renderWith(scenarios = [support, drafts], version = 4) {
  vi.mocked(mddbClient.listChatScenarios).mockResolvedValue({ scenarios, version });
  const view = render(<ChatScenariosPanel />);
  await screen.findByText(`version ${version}`);
  return view;
}

const field = (label) => screen.getByLabelText(label);
const type = (label, value) => fireEvent.change(field(label), { target: { value } });

beforeEach(() => {
  useStore.setState({ ...initialStore, stats: { collections: [{ name: 'docs' }, { name: 'runbooks' }] } }, true);
  vi.mocked(mddbClient.getChatScenarioPrompt).mockResolvedValue({ name: 'support', prompt: 'You are Ada.\n\nDo not use emoji.', version: 4 });
  vi.mocked(mddbClient.putChatScenario).mockResolvedValue({});
  vi.mocked(mddbClient.deleteChatScenario).mockResolvedValue({ deleted: true });
});
afterEach(cleanup);

describe('the list', () => {
  test('shows each scenario, marks a disabled one, and waits for a pick', async () => {
    await renderWith();
    const nav = screen.getByRole('navigation', { name: 'Scenarios' });
    expect(within(nav).getByText('support')).toBeTruthy();
    expect(within(nav).getByText('Support desk')).toBeTruthy();
    expect(within(nav).getByRole('button', { name: /drafts/ }).textContent).toContain('off');
    expect(screen.getByText('Pick a scenario to edit it, or create a new one.')).toBeTruthy();
  });

  test('an empty server says so; a server without the fields lists nothing', async () => {
    vi.mocked(mddbClient.listChatScenarios).mockResolvedValue({});
    render(<ChatScenariosPanel />);
    expect(await screen.findByText('No scenarios yet.')).toBeTruthy();
    expect(screen.getByText('version 0')).toBeTruthy();
  });

  test('a failed load shows the server message', async () => {
    vi.mocked(mddbClient.listChatScenarios).mockRejectedValue(new Error('HTTP 403: forbidden'));
    render(<ChatScenariosPanel />);
    expect((await screen.findByRole('alert')).textContent).toBe('HTTP 403: forbidden');
  });
});

describe('editing a stored scenario', () => {
  test('opens it with its fields, a locked name, the preview and the placeholder', async () => {
    await renderWith();
    fireEvent.click(screen.getByRole('button', { name: /support/ }));
    expect(field('Name').value).toBe('support');
    expect(field('Name').disabled).toBe(true);
    expect(field('Operator prompt').value).toBe('You are Ada.');
    expect(field('docs').checked).toBe(true);
    expect(field('runbooks').checked).toBe(false);
    expect(field('kb-alias').checked).toBe(true); // stored, not a collection here: kept
    expect(field('Extra field 1 name').value).toBe('reasoning_effort');
    expect(screen.getByRole('button', { name: /support/ }).getAttribute('aria-current')).toBe('true');
    expect((await screen.findByText(/Do not use emoji\./)).tagName).toBe('PRE');
    expect(mddbClient.getChatScenarioPrompt).toHaveBeenCalledWith('support');
    expect(screen.getByRole('heading', { name: 'Try it' })).toBeTruthy();
    expect(screen.getByText(/needs the chat service \(mddb-chat\) connected/)).toBeTruthy();
  });

  test('saves every changed field as one scenario and reloads', async () => {
    await renderWith();
    fireEvent.click(screen.getByRole('button', { name: /support/ }));
    type('Description', 'Help desk');
    fireEvent.click(field(/Enabled/));
    type('Guardrails', 'No prices.');
    fireEvent.click(field('runbooks'));
    fireEvent.click(field('kb-alias'));
    type('Skills', 'triage, refunds');
    type('Off-topic questions', 'redirect');
    type('Redirect reply', 'Ask about MDDB.');
    type('Emoji', 'free');
    type('Answer length', 'long');
    type('Citation format', '[{{key}}]');
    fireEvent.click(field('Format answers in Markdown'));
    type('Model', 'gpt-y');
    type('Temperature', '0');
    type('Max answer tokens', '900');
    type('Extra field 1 value', 'high');
    fireEvent.click(screen.getByRole('button', { name: 'Add field' }));
    type('Extra field 2 name', 'top_k');
    type('Extra field 2 value', '40');
    type('Max turns per session', '8');
    type('Token budget per session', '20000');
    type('Messages per minute', '10');
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));

    expect((await screen.findByRole('status')).textContent).toBe('Saved support. The chat service picks it up on its next poll.');
    expect(mddbClient.putChatScenario).toHaveBeenCalledWith('support', {
      description: 'Help desk',
      enabled: false,
      operatorPrompt: 'You are Ada.',
      guardrails: 'No prices.',
      offTopic: { mode: 'redirect', redirectTemplate: 'Ask about MDDB.' },
      allowedCollections: ['docs', 'runbooks'],
      skills: ['triage', 'refunds'],
      style: { emoji: 'free', verbosity: 'long', markdown: false, citationTemplate: '[{{key}}]' },
      llm: { model: 'gpt-y', maxTokens: 900, temperature: 0, extraBody: { reasoning_effort: 'high', top_k: 40 } },
      maxTurns: 8,
      tokenBudgetPerSession: 20000,
      rateLimit: { messagesPerMinute: 10 },
    });
    expect(mddbClient.listChatScenarios).toHaveBeenCalledTimes(2);
  });

  test('an extra field can be removed; with none left the hint returns', async () => {
    await renderWith();
    fireEvent.click(screen.getByRole('button', { name: /support/ }));
    fireEvent.click(screen.getByRole('button', { name: 'Remove extra field 1' }));
    expect(screen.getByText('Nothing extra is sent to the model API.')).toBeTruthy();
  });

  test('a refused save shows the server message and keeps the form', async () => {
    vi.mocked(mddbClient.putChatScenario).mockRejectedValue(new Error('HTTP 400: collection "x" does not exist'));
    await renderWith();
    fireEvent.click(screen.getByRole('button', { name: /support/ }));
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    expect((await screen.findByRole('alert')).textContent).toBe('HTTP 400: collection "x" does not exist');
    expect(field('Name').value).toBe('support');
    await waitFor(() => expect(screen.getByRole('button', { name: 'Save' }).disabled).toBe(false));
  });

  test('deletes after a confirm, and not without one', async () => {
    const confirmSpy = vi.fn(() => false);
    vi.stubGlobal('confirm', confirmSpy);
    await renderWith();
    fireEvent.click(screen.getByRole('button', { name: /support/ }));
    fireEvent.click(screen.getByRole('button', { name: 'Delete' }));
    expect(confirmSpy.mock.calls[0][0]).toContain('Delete the scenario "support"?');
    expect(mddbClient.deleteChatScenario).not.toHaveBeenCalled();

    confirmSpy.mockReturnValue(true);
    fireEvent.click(screen.getByRole('button', { name: 'Delete' }));
    expect((await screen.findByRole('status')).textContent).toBe('Deleted support.');
    expect(mddbClient.deleteChatScenario).toHaveBeenCalledWith('support');
    expect(screen.getByText('Pick a scenario to edit it, or create a new one.')).toBeTruthy();
  });
});

describe('a new scenario', () => {
  test('needs a valid name before anything is sent', async () => {
    await renderWith([]);
    fireEvent.click(screen.getByRole('button', { name: 'New scenario' }));
    expect(field('Name').disabled).toBe(false);
    expect(screen.getByText('Save the scenario to see its composed prompt.')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Delete' })).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    expect(screen.getByRole('alert').textContent).toBe('Give the scenario a name.');
    expect(mddbClient.putChatScenario).not.toHaveBeenCalled();
  });

  test('is saved under its trimmed name and then shows its preview', async () => {
    await renderWith([]);
    fireEvent.click(screen.getByRole('button', { name: 'New scenario' }));
    type('Name', ' sales ');
    type('Operator prompt', 'You sell.');
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    await screen.findByRole('status');
    expect(vi.mocked(mddbClient.putChatScenario).mock.calls[0][0]).toBe('sales');
    expect(vi.mocked(mddbClient.putChatScenario).mock.calls[0][1].operatorPrompt).toBe('You sell.');
    await waitFor(() => expect(mddbClient.getChatScenarioPrompt).toHaveBeenCalledWith('sales'));
    expect(screen.getByRole('button', { name: 'Delete' })).toBeTruthy();
  });

  test('a server without collections says so in the picker', async () => {
    useStore.setState({ stats: null });
    await renderWith([]);
    fireEvent.click(screen.getByRole('button', { name: 'New scenario' }));
    expect(screen.getByText('This server has no collections yet.')).toBeTruthy();
  });
});
