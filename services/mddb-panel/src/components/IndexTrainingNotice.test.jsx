// @vitest-environment jsdom
// IndexTrainingNotice: the banner shown while ivf/pq/opq/sq/sq4 indexes train
// after a restart (GO-047) — hidden with an empty queue, otherwise the indexes,
// the queue, the collection in training and the 503 warning.
import { describe, test, expect, afterEach } from 'vitest';
import { render, screen, cleanup } from '@testing-library/react';
import IndexTrainingNotice from './IndexTrainingNotice';

afterEach(cleanup);

describe('IndexTrainingNotice', () => {
  test.each([
    ['undefined', undefined],
    ['null', null],
    ['no pending list', { current: 'blog' }],
    ['an empty queue', { current: '', pending: [], indexes: ['ivf'] }],
  ])('renders nothing for %s', (_label, training) => {
    const { container } = render(<IndexTrainingNotice training={training} />);
    expect(container.innerHTML).toBe('');
  });

  test('names the indexes, the queue and the collection in training', () => {
    render(
      <IndexTrainingNotice training={{ current: 'blog', pending: ['blog', 'docs'], indexes: ['ivf', 'pq'] }} />,
    );
    const status = screen.getByRole('status');
    // A native <output>: a polite live region without an explicit role.
    expect(status.tagName).toBe('OUTPUT');
    expect(status.querySelector('.font-medium').textContent).toBe('Training ivf, pq for 2 collections (now: blog)');
    expect(status.textContent).toContain('A search naming ivf, pq on blog, docs is refused with 503');
  });

  test.each([
    ['no index list', { pending: ['blog'] }],
    ['an empty index list', { pending: ['blog'], indexes: [], current: '' }],
  ])('falls back to "trainable indexes" with %s, singular and no current', (_label, training) => {
    render(<IndexTrainingNotice training={training} />);
    const status = screen.getByRole('status');
    expect(status.querySelector('.font-medium').textContent).toBe('Training trainable indexes for 1 collection');
    expect(status.textContent).toContain('A search naming trainable indexes on blog is refused with 503');
  });
});
