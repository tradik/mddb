// chat-log (#406): the Log tab's filters as a query, and how a turn reads.
import { describe, test, expect } from 'vitest';
import { emptyLogFilters, gapParams, logParams, outcomeLabels, ratingText, scoreText, whoOf } from './chat-log';

describe('logParams', () => {
  test('a fresh filter asks for everything', () => {
    expect(logParams(emptyLogFilters)).toEqual({ scenario: '', from: '', to: '', rating: '', outcome: '' });
  });

  test('thumbs-down is the down rating; the shortcuts win over the outcome list', () => {
    const f = { ...emptyLogFilters, scenario: 's', from: '2026-11-01', to: '2026-11-02', thumbsDown: true, outcome: 'error' };
    expect(logParams(f)).toEqual({ scenario: 's', from: '2026-11-01', to: '2026-11-02', rating: 'down', outcome: 'error' });
    expect(logParams({ ...f, refused: true }).outcome).toBe('refused');
    expect(logParams({ ...f, refused: true, noResults: true }).outcome).toBe('noResults');
  });

  test('gapParams keeps the window and adds the gap settings', () => {
    expect(gapParams({ ...emptyLogFilters, scenario: 's' }, { minCount: '2', threshold: '' }))
      .toEqual({ scenario: 's', from: '', to: '', minCount: '2', threshold: '' });
  });
});

describe('a turn as shown', () => {
  test.each([
    [{ anonymous: true, sub: 'x' }, 'anonymous'],
    [{ sub: '' }, 'anonymous'],
    [{ sub: 'u1' }, 'u1'],
    [{ sub: 'u1', displayName: 'Ada' }, 'Ada (u1)'],
  ])('%j asked by %s', (turn, who) => {
    expect(whoOf(turn)).toBe(who);
  });

  test('ratings, scores and outcomes', () => {
    expect([ratingText('up'), ratingText('down'), ratingText(undefined)]).toEqual(['Thumbs up', 'Thumbs down', '—']);
    expect(scoreText({ resultKeys: ['a'], topScore: 0.8234 })).toBe('0.82');
    expect(scoreText({ resultKeys: ['a'] })).toBe('0.00');
    expect(scoreText({ resultKeys: [] })).toBe('—');
    expect(scoreText({})).toBe('—');
    expect(Object.keys(outcomeLabels)).toEqual(['answered', 'noResults', 'refused', 'error']);
  });
});
