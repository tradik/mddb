// @vitest-environment jsdom
// MetaDiff (FE-013): the metadata keys that differ, named in words.
import { describe, test, expect, afterEach } from 'vitest';
import { render, screen, cleanup } from '@testing-library/react';
import MetaDiff from './MetaDiff';

afterEach(cleanup);

describe('MetaDiff', () => {
  test('lists added, removed and changed keys with their values', () => {
    render(<MetaDiff label="Metadata" before={{ a: ['1'], b: ['x'], c: ['gone'] }} after={{ a: ['2'], b: ['x'], d: ['new'] }} />);
    expect(screen.getByText('Metadata: 3 keys differ')).toBeTruthy();
    const items = screen.getAllByRole('listitem').map((li) => li.textContent);
    expect(items).toEqual(['changed a: 1 → 2', 'removed c: gone', 'added d: new']);
  });

  test('one key, and no change at all', () => {
    const { rerender } = render(<MetaDiff label="M" before={{}} after={{ a: 'x' }} />);
    expect(screen.getByText('M: 1 key differ')).toBeTruthy();
    rerender(<MetaDiff label="M" before={{ a: ['x'] }} after={{ a: ['x'] }} />);
    expect(screen.getByText('No metadata change.')).toBeTruthy();
    expect(screen.queryByRole('list')).toBeNull();
  });
});
