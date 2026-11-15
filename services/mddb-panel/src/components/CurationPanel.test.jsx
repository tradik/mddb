// @vitest-environment jsdom
// CurationPanel: the curation rules of the current collection — no collection,
// loading, empty, a load error, the rule rows (enabled/disabled, pins, hides),
// deleting (confirm, refusal, error) and the rule form (create, edit, pins,
// hides, validation, save errors, busy state, cancel and close).
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, act, waitFor } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({
  default: {
    listCurationRules: vi.fn(),
    deleteCurationRule: vi.fn(),
    createCurationRule: vi.fn(),
    updateCurationRule: vi.fn(),
  },
}));

const { default: mddbClient } = await import('../lib/mddb-client');
const { useStore } = await import('../lib/store');
const { default: CurationPanel } = await import('./CurationPanel');
const client = vi.mocked(mddbClient);

const initialStore = useStore.getState();

const ruleA = {
  id: 'r1', collection: 'docs', query: 'rust tutorial', matchMode: 'contains', enabled: true,
  pins: [{ key: 'intro', lang: 'en', position: 1 }, { key: 'setup', position: 2 }],
  hides: ['old-post', 'draft'],
};
const ruleB = { id: 'r2', collection: 'docs', query: 'go', enabled: false, pins: [], hides: [] };
const ruleC = { id: 'r3', collection: 'docs', query: 'bare', enabled: true };

beforeEach(() => {
  useStore.setState(initialStore, true);
  vi.clearAllMocks();
  client.listCurationRules.mockResolvedValue({ rules: [] });
  client.deleteCurationRule.mockResolvedValue({});
  client.createCurationRule.mockResolvedValue({});
  client.updateCurationRule.mockResolvedValue({});
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

async function renderPanel(collection = 'docs') {
  useStore.setState({ currentCollection: collection });
  await act(async () => {
    render(<CurationPanel />);
  });
}

// The form's footer Save button (the rule rows use title="Edit" instead).
const saveButton = () => screen.getByRole('button', { name: /^(Save|Saving…)$/ });
const queryInput = () => screen.getByPlaceholderText('e.g. rust tutorial');
const hideInput = () => screen.getAllByPlaceholderText('document key').at(-1);
// Both the pin and the hide button read "Add"; tell them apart by their section.
const pinAdd = () => screen.getByText('Pinned Documents').parentElement.querySelector('button');
const hideAdd = () => hideInput().parentElement.querySelector('button');

describe('CurationPanel list', () => {
  test('asks for a collection when none is selected and loads nothing', async () => {
    await act(async () => {
      render(<CurationPanel />);
    });
    expect(screen.getByText('Select a collection to manage its curation rules.')).toBeTruthy();
    expect(client.listCurationRules).not.toHaveBeenCalled();
  });

  test('shows Loading… while the rules load', async () => {
    let resolve;
    client.listCurationRules.mockReturnValue(new Promise((r) => { resolve = r; }));
    await renderPanel();
    expect(screen.getByText('Loading…')).toBeTruthy();
    await act(async () => resolve({ rules: [] }));
    expect(screen.queryByText('Loading…')).toBeNull();
  });

  test('an empty list, and a response without rules, says there are none', async () => {
    client.listCurationRules.mockResolvedValue({});
    await renderPanel();
    expect(client.listCurationRules).toHaveBeenCalledWith('docs');
    expect(screen.getByText(/No curation rules yet/)).toBeTruthy();
    expect(screen.getByText('docs')).toBeTruthy();
  });

  test('a load error is shown', async () => {
    client.listCurationRules.mockRejectedValue(new Error('collection not found'));
    await renderPanel();
    expect(screen.getByText('collection not found')).toBeTruthy();
  });

  test('rows show state, query, mode, pins and hides', async () => {
    client.listCurationRules.mockResolvedValue({ rules: [ruleA, ruleB, ruleC] });
    await renderPanel();
    expect(screen.getAllByText('ENABLED')).toHaveLength(2);
    expect(screen.getByText('DISABLED').className).toContain('bg-surface-strong');
    expect(screen.getAllByText('ENABLED')[0].className).toContain('bg-success-muted');
    expect(screen.getByText('rust tutorial')).toBeTruthy();
    expect(screen.getByText('(contains)')).toBeTruthy();
    expect(screen.getAllByText('(exact)')).toHaveLength(2);
    expect(screen.getByText('#1 intro, #2 setup')).toBeTruthy();
    expect(screen.getByText('old-post, draft')).toBeTruthy();
    expect(screen.getAllByText('Pins:')).toHaveLength(1);
    expect(screen.getAllByText('Hides:')).toHaveLength(1);
  });
});

describe('CurationPanel delete', () => {
  test('deletes after confirmation and reloads', async () => {
    client.listCurationRules.mockResolvedValue({ rules: [ruleA] });
    vi.spyOn(window, 'confirm').mockReturnValue(true);
    await renderPanel();
    client.listCurationRules.mockResolvedValue({ rules: [] });
    await act(async () => {
      fireEvent.click(screen.getByTitle('Delete'));
    });
    expect(window.confirm).toHaveBeenCalledWith('Delete this curation rule?');
    expect(client.deleteCurationRule).toHaveBeenCalledWith('r1');
    expect(client.listCurationRules).toHaveBeenCalledTimes(2);
    expect(screen.getByText(/No curation rules yet/)).toBeTruthy();
  });

  test('does nothing when the confirmation is declined', async () => {
    client.listCurationRules.mockResolvedValue({ rules: [ruleA] });
    vi.spyOn(window, 'confirm').mockReturnValue(false);
    await renderPanel();
    await act(async () => {
      fireEvent.click(screen.getByTitle('Delete'));
    });
    expect(client.deleteCurationRule).not.toHaveBeenCalled();
  });

  test('a delete error is shown', async () => {
    client.listCurationRules.mockResolvedValue({ rules: [ruleA] });
    client.deleteCurationRule.mockRejectedValue(new Error('forbidden'));
    vi.spyOn(window, 'confirm').mockReturnValue(true);
    await renderPanel();
    await act(async () => {
      fireEvent.click(screen.getByTitle('Delete'));
    });
    expect(screen.getByText('forbidden')).toBeTruthy();
  });
});

describe('CurationRuleForm', () => {
  test('a new rule: validation, pins, hides, save and reload', async () => {
    await renderPanel();
    fireEvent.click(screen.getByRole('button', { name: 'New Rule' }));
    expect(screen.getByText('New Curation Rule')).toBeTruthy();
    expect(screen.getByText('No pins')).toBeTruthy();
    expect(screen.getByRole('checkbox').checked).toBe(true);

    // An empty (blank) query is refused before any request.
    fireEvent.change(queryInput(), { target: { value: '   ' } });
    await act(async () => {
      fireEvent.click(saveButton());
    });
    expect(screen.getByText('query is required')).toBeTruthy();
    expect(client.createCurationRule).not.toHaveBeenCalled();

    fireEvent.change(queryInput(), { target: { value: '  rust  ' } });
    fireEvent.change(screen.getByRole('combobox'), { target: { value: 'contains' } });
    fireEvent.click(screen.getByRole('checkbox'));

    // Two pins: one filled in, one left blank (dropped on save), one removed.
    fireEvent.click(pinAdd());
    fireEvent.click(pinAdd());
    fireEvent.click(pinAdd());
    const positions = screen.getAllByPlaceholderText('#');
    expect(positions.map((p) => p.value)).toEqual(['1', '2', '3']);
    const keys = screen.getAllByPlaceholderText('document key');
    fireEvent.change(keys[0], { target: { value: 'intro' } });
    fireEvent.change(positions[0], { target: { value: '5' } });
    fireEvent.change(screen.getAllByPlaceholderText('lang (optional)')[0], { target: { value: 'en' } });
    // Remove the third pin: its trash button is the last button in its row.
    const thirdRow = positions[2].parentElement;
    fireEvent.click(thirdRow.querySelector('button'));
    expect(screen.getAllByPlaceholderText('#')).toHaveLength(2);

    // Hides: blank is ignored, Enter and the Add button add, duplicates are ignored, X removes.
    fireEvent.click(hideAdd());
    fireEvent.change(hideInput(), { target: { value: 'old' } });
    fireEvent.keyDown(hideInput(), { key: 'Enter' });
    expect(hideInput().value).toBe('');
    fireEvent.change(hideInput(), { target: { value: 'other' } });
    fireEvent.keyDown(hideInput(), { key: 'a' });
    expect(hideInput().value).toBe('other');
    fireEvent.click(hideAdd());
    fireEvent.change(hideInput(), { target: { value: 'old' } });
    fireEvent.click(hideAdd());
    expect(hideInput().value).toBe('old');
    fireEvent.change(hideInput(), { target: { value: 'gone' } });
    fireEvent.click(hideAdd());
    fireEvent.click(screen.getByText('gone').querySelector('button'));
    expect(screen.queryByText('gone')).toBeNull();

    await act(async () => {
      fireEvent.click(saveButton());
    });
    expect(client.createCurationRule).toHaveBeenCalledWith({
      collection: 'docs',
      query: 'rust',
      matchMode: 'contains',
      enabled: false,
      pins: [{ key: 'intro', lang: 'en', position: 5 }],
      hides: ['old', 'other'],
    });
    expect(client.updateCurationRule).not.toHaveBeenCalled();
    expect(screen.queryByText('New Curation Rule')).toBeNull();
    expect(client.listCurationRules).toHaveBeenCalledTimes(2);
  });

  test('the pin and hide sections each have an Add button', async () => {
    await renderPanel();
    fireEvent.click(screen.getByRole('button', { name: 'New Rule' }));
    expect(screen.getAllByRole('button', { name: 'Add' })).toEqual([pinAdd(), hideAdd()]);
  });

  test('editing a rule prefills it and updates it', async () => {
    client.listCurationRules.mockResolvedValue({ rules: [ruleA] });
    await renderPanel();
    fireEvent.click(screen.getByTitle('Edit'));
    expect(screen.getByText('Edit Curation Rule')).toBeTruthy();
    expect(queryInput().value).toBe('rust tutorial');
    expect(screen.getByRole('combobox').value).toBe('contains');
    expect(screen.getAllByPlaceholderText('lang (optional)').map((i) => i.value)).toEqual(['en', '']);
    expect(screen.getByText('old-post')).toBeTruthy();
    await act(async () => {
      fireEvent.click(saveButton());
    });
    expect(client.updateCurationRule).toHaveBeenCalledWith({ ...ruleA });
    expect(client.createCurationRule).not.toHaveBeenCalled();
  });

  test('a rule without an id, mode, pins or hides opens with defaults and is created', async () => {
    const noId = { query: 'loose', enabled: true };
    client.listCurationRules.mockResolvedValue({ rules: [{ ...noId, id: undefined }] });
    await renderPanel();
    fireEvent.click(screen.getByTitle('Edit'));
    expect(screen.getByText('New Curation Rule')).toBeTruthy();
    expect(screen.getByRole('combobox').value).toBe('exact');
    expect(screen.getByText('No pins')).toBeTruthy();
    await act(async () => {
      fireEvent.click(saveButton());
    });
    expect(client.createCurationRule).toHaveBeenCalledWith({
      id: undefined, query: 'loose', enabled: true, collection: 'docs', matchMode: 'exact', pins: [], hides: [],
    });
  });

  test('a disabled rule opens unchecked', async () => {
    client.listCurationRules.mockResolvedValue({ rules: [ruleB] });
    await renderPanel();
    fireEvent.click(screen.getByTitle('Edit'));
    expect(screen.getByRole('checkbox').checked).toBe(false);
  });

  test('a save error is shown and the form stays open; Save is busy meanwhile', async () => {
    let reject;
    client.createCurationRule.mockReturnValue(new Promise((_, r) => { reject = r; }));
    await renderPanel();
    fireEvent.click(screen.getByRole('button', { name: 'New Rule' }));
    fireEvent.change(queryInput(), { target: { value: 'rust' } });
    await act(async () => {
      fireEvent.click(saveButton());
    });
    expect(saveButton().textContent).toBe('Saving…');
    expect(saveButton().disabled).toBe(true);
    await act(async () => reject(new Error('duplicate rule')));
    expect(screen.getByText('duplicate rule')).toBeTruthy();
    expect(saveButton().textContent).toBe('Save');
    expect(screen.getByText('New Curation Rule')).toBeTruthy();
  });

  test('Cancel closes the form', async () => {
    await renderPanel();
    fireEvent.click(screen.getByRole('button', { name: 'New Rule' }));
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(screen.queryByText('New Curation Rule')).toBeNull();
  });

  test('the X in the header closes the form', async () => {
    await renderPanel();
    fireEvent.click(screen.getByRole('button', { name: 'New Rule' }));
    const heading = screen.getByRole('heading', { name: 'New Curation Rule' });
    fireEvent.click(heading.parentElement.querySelector('button'));
    await waitFor(() => expect(screen.queryByText('New Curation Rule')).toBeNull());
  });
});
