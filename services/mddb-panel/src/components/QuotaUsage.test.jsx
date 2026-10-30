// @vitest-environment jsdom
// QuotaUsage: one bar per quota limit, with the numbers as visible text and
// as the bar's text alternative, the state in words, and nothing without a quota.
import { describe, test, expect, afterEach } from 'vitest';
import { render, screen, cleanup } from '@testing-library/react';
import QuotaUsage from './QuotaUsage';

afterEach(cleanup);

describe('QuotaUsage', () => {
  test('renders nothing without a quota or usage', () => {
    const { container } = render(<QuotaUsage quota={{ maxDocuments: 5 }} />);
    expect(container.innerHTML).toBe('');
  });

  test('a bar per limit, named, with its numbers as text and as aria-valuetext', () => {
    render(
      <QuotaUsage
        quota={{ maxDocuments: 5000, maxVectors: 100, embeddingCallsPerDay: 10 }}
        usage={{ documents: 1234, vectors: 85, embeddingCalls: 12 }}
      />,
    );
    expect(screen.getByRole('heading', { name: 'Quota usage' })).toBeTruthy();
    expect(screen.getByText('1,234 / 5,000 documents')).toBeTruthy();

    const docs = screen.getByRole('progressbar', { name: 'Documents' });
    expect(docs.getAttribute('aria-valuenow')).toBe('25');
    expect(docs.getAttribute('aria-valuetext')).toBe('1,234 / 5,000 documents (25%)');
    expect(docs.firstChild.className).toContain('bg-primary-700');
    expect(docs.firstChild.style.width).toBe('25%');

    const vectors = screen.getByRole('progressbar', { name: 'Vectors' });
    expect(vectors.firstChild.className).toContain('bg-amber-700');
    expect(screen.getByText('Close to the limit.')).toBeTruthy();

    // Over the limit: the bar stops at 100%, the text keeps the real share.
    const calls = screen.getByRole('progressbar', { name: 'Embedding calls today' });
    expect(calls.getAttribute('aria-valuenow')).toBe('100');
    expect(calls.getAttribute('aria-valuetext')).toBe('12 / 10 embedding calls today (120%)');
    expect(calls.firstChild.className).toContain('bg-red-700');
    expect(calls.firstChild.style.width).toBe('100%');
    expect(screen.getByText('120%')).toBeTruthy();
    expect(screen.getByText(/At or over the limit/).className).toContain('text-red-700');
  });
});
