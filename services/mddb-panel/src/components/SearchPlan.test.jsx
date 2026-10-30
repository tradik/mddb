// @vitest-environment jsdom
// SearchPlan: the "How this search ran" box — index, filter strategy,
// candidates, relevance feedback, query parts, reasons, and one plan per
// target collection for a cross-collection search.
import { describe, test, expect, afterEach } from 'vitest';
import { render, screen, cleanup, within } from '@testing-library/react';
import SearchPlan from './SearchPlan';

afterEach(cleanup);

/** The text of the <dd> right after the <dt> labelled `label`. */
function valueOf(label, root = document) {
  const dt = [...root.querySelectorAll('dt')].find((el) => el.textContent === label);
  return dt.nextElementSibling.textContent;
}

describe('SearchPlan', () => {
  test.each([null, undefined])('renders nothing for plan=%s', (plan) => {
    const { container } = render(<SearchPlan plan={plan} />);
    expect(container.innerHTML).toBe('');
  });

  test('a minimal plan: post-filter, no feedback, no parts, no reasons', () => {
    render(<SearchPlan plan={{ algorithm: 'hnsw', prefilter: false, candidates: 40 }} />);
    const section = screen.getByRole('region', { name: 'Search plan' });
    expect(within(section).getByText('How this search ran')).toBeTruthy();
    expect(valueOf('Index')).toBe('hnsw');
    expect(valueOf('Filter')).toBe('after the index search');
    expect(valueOf('Candidates')).toBe('40');
    expect(screen.queryByText('Feedback')).toBeNull();
    expect(screen.queryByText('Query parts')).toBeNull();
    expect(section.querySelector('ul')).toBeNull();
  });

  test('a full plan: prefilter, widened scan, query feedback, split parts, reasons', () => {
    render(
      <SearchPlan
        plan={{
          algorithm: 'flat',
          prefilter: true,
          candidates: 10,
          iterativeScan: true,
          feedback: { mode: 'query', positive: 2, negative: 1, queryShift: 0.12345 },
          decomposition: { parts: 2, texts: ['cats', 'dogs'] },
          reasons: ['small collection', 'filter is selective'],
        }}
      />,
    );
    expect(valueOf('Filter')).toBe('applied first, exact scan');
    expect(valueOf('Candidates')).toBe('10 (scan widened: the filtered index search came back short)');
    expect(valueOf('Feedback')).toBe('query: 2 positive, 1 negative, query moved 0.123');
    expect(valueOf('Query parts')).toBe('cats · dogs');
    expect(screen.getAllByRole('listitem').map((li) => li.textContent)).toEqual([
      'small collection',
      'filter is selective',
    ]);
  });

  test.each([
    ['query mode with no shift', { mode: 'query', positive: 1, negative: 0 }, 'query: 1 positive, 0 negative, query moved 0.000'],
    ['rescore mode with a count', { mode: 'rescore', positive: 0, negative: 3, rescored: 7 }, 'rescore: 0 positive, 3 negative, 7 rescored'],
    ['rescore mode without a count', { mode: 'rescore', positive: 1, negative: 1 }, 'rescore: 1 positive, 1 negative, 0 rescored'],
  ])('feedback line for %s', (_label, feedback, expected) => {
    render(<SearchPlan plan={{ algorithm: 'flat', candidates: 5, feedback }} />);
    expect(valueOf('Feedback')).toBe(expected);
  });

  test('a query that was not split says so', () => {
    render(<SearchPlan plan={{ algorithm: 'flat', candidates: 5, decomposition: { parts: 1, texts: ['x'] }, reasons: [] }} />);
    expect(valueOf('Query parts')).toBe('not split');
    expect(screen.queryByRole('list')).toBeNull();
  });

  test('a cross-collection plan shows one block per target and no top-level body', () => {
    render(
      <SearchPlan
        plan={{
          collections: {
            docs: { algorithm: 'hnsw', candidates: 20 },
            blog: { algorithm: 'flat', prefilter: true, candidates: 5, reasons: ['tiny'] },
          },
        }}
      />,
    );
    const docs = screen.getByText('docs').parentElement;
    const blog = screen.getByText('blog').parentElement;
    expect(valueOf('Index', docs)).toBe('hnsw');
    expect(valueOf('Filter', docs)).toBe('after the index search');
    expect(valueOf('Index', blog)).toBe('flat');
    expect(valueOf('Filter', blog)).toBe('applied first, exact scan');
    expect(within(blog).getByText('tiny')).toBeTruthy();
    expect(screen.getAllByText('Index')).toHaveLength(2);
  });
});
