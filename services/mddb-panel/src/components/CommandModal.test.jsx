// @vitest-environment jsdom
// CommandModal: the "API Command" dialog — closed renders nothing, the four
// language tabs (curl, PHP, Python, JavaScript) for every request type with
// defaults and with explicit params, the bm25f field weights, an unknown type,
// and the Copy button (clipboard, execCommand fallback, the 2 s "Copied").
import { describe, test, expect, vi, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, act } from '@testing-library/react';

const { default: CommandModal } = await import('./CommandModal');

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  delete navigator.clipboard;
});

const TABS = ['curl', 'PHP', 'Python', 'JavaScript'];

const full = {
  search: { collection: 'docs', filterMeta: { tag: ['go'] }, sort: 'updatedAt', asc: true, limit: 5, offset: 10 },
  fts: {
    collection: 'docs', query: 'rust', limit: 7, algorithm: 'bm25f', fuzzy: 2,
    disableStem: true, disableSynonyms: true, fieldWeights: { title: 3 },
  },
  vector: { collection: 'docs', query: 'cats', topK: 3, threshold: 0.4, algorithm: 'hnsw', includeContent: true },
  hybrid: {
    collection: 'docs', query: 'dogs', topK: 4, algorithm: 'bm25f', vectorAlgorithm: 'ivf',
    alpha: 0.2, strategy: 'rrf', rrfK: 30, fuzzy: 1, threshold: 0.3, includeContent: true,
  },
};

// What each tab must contain for a type: its endpoint and, per params, a value.
const endpoints = { search: '/search', fts: '/fts', vector: '/vector-search', hybrid: '/hybrid-search', other: '/search' };

function renderModal(type, params, onClose = vi.fn()) {
  render(<CommandModal isOpen onClose={onClose} type={type} params={params} />);
  return onClose;
}

function codeText(container) {
  return container.querySelector('code').textContent;
}

describe('CommandModal', () => {
  test('renders nothing when closed', () => {
    const { container } = render(<CommandModal isOpen={false} onClose={vi.fn()} type="search" params={{}} />);
    expect(container.innerHTML).toBe('');
  });

  test('the close button calls onClose', () => {
    const onClose = renderModal('search', {});
    fireEvent.click(screen.getAllByRole('button')[0]);
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  test.each(Object.keys(full))('%s with defaults on every tab', (type) => {
    const { container } = render(<CommandModal isOpen onClose={vi.fn()} type={type} params={{}} />);
    for (const tab of TABS) {
      fireEvent.click(screen.getByRole('button', { name: tab }));
      expect(screen.getByRole('button', { name: tab }).className).toContain('bg-gray-900');
      const text = codeText(container);
      expect(text).toContain(`http://localhost:11023/v1${endpoints[type]}`);
      expect(text).toContain('my-collection');
      expect(text).not.toContain('fieldWeights');
    }
  });

  test.each(Object.keys(full))('%s with explicit params on every tab', (type) => {
    const { container } = render(<CommandModal isOpen onClose={vi.fn()} type={type} params={full[type]} />);
    for (const tab of TABS) {
      fireEvent.click(screen.getByRole('button', { name: tab }));
      const text = codeText(container);
      expect(text).toContain(endpoints[type]);
      expect(text).toContain('docs');
      expect(text).not.toContain('my-collection');
      if (type === 'fts') expect(text).toContain('fieldWeights');
    }
  });

  test('bm25f without field weights leaves them out on every tab', () => {
    const { container } = render(
      <CommandModal isOpen onClose={vi.fn()} type="fts" params={{ algorithm: 'bm25f' }} />,
    );
    for (const tab of TABS) {
      fireEvent.click(screen.getByRole('button', { name: tab }));
      expect(codeText(container)).toContain('bm25f');
      expect(codeText(container)).not.toContain('fieldWeights');
    }
  });

  test('an unknown type posts an empty body to /search on every tab', () => {
    const { container } = render(<CommandModal isOpen onClose={vi.fn()} type="other" params={{}} />);
    const expected = {
      curl: "-d '{}'",
      PHP: '$data = [];',
      Python: 'payload = {}',
      JavaScript: 'body: JSON.stringify({})',
    };
    for (const tab of TABS) {
      fireEvent.click(screen.getByRole('button', { name: tab }));
      expect(codeText(container)).toContain(endpoints.other);
      expect(codeText(container)).toContain(expected[tab]);
    }
  });

  test('curl is the default tab and shows a curl command', () => {
    const { container } = render(<CommandModal isOpen onClose={vi.fn()} type="search" params={{}} />);
    expect(codeText(container)).toMatch(/^curl -X POST/);
    expect(screen.getByRole('button', { name: 'PHP' }).className).toContain('text-gray-600');
  });

  test('Copy writes the code to the clipboard and says Copied for 2 s', async () => {
    vi.useFakeTimers();
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true });
    const { container } = render(<CommandModal isOpen onClose={vi.fn()} type="vector" params={{}} />);
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Copy' }));
    });
    expect(writeText).toHaveBeenCalledWith(codeText(container));
    expect(screen.getByRole('button', { name: 'Copied' })).toBeTruthy();
    act(() => vi.advanceTimersByTime(2000));
    expect(screen.getByRole('button', { name: 'Copy' })).toBeTruthy();
  });

  test('Copy falls back to execCommand when the clipboard API fails', async () => {
    vi.useFakeTimers();
    const writeText = vi.fn().mockRejectedValue(new Error('denied'));
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true });
    const execCommand = vi.fn().mockReturnValue(true);
    document.execCommand = execCommand;
    let copiedValue;
    execCommand.mockImplementation(() => {
      copiedValue = document.activeElement?.value ?? document.querySelector('textarea')?.value;
      return true;
    });
    const { container } = render(<CommandModal isOpen onClose={vi.fn()} type="hybrid" params={{}} />);
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Copy' }));
    });
    expect(execCommand).toHaveBeenCalledWith('copy');
    expect(copiedValue).toBe(codeText(container));
    expect(document.querySelector('textarea')).toBeNull();
    expect(screen.getByRole('button', { name: 'Copied' })).toBeTruthy();
    act(() => vi.advanceTimersByTime(2000));
    expect(screen.getByRole('button', { name: 'Copy' })).toBeTruthy();
    delete document.execCommand;
  });
});
