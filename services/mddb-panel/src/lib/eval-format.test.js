import { describe, test, expect } from 'vitest';
import {
  EVAL_PRESETS, MAX_EVAL_CONFIGS, parseKeys, parseGrades, formatGrades, clampK, bestConfig, formatScore,
} from './eval-format';

describe('eval-format', () => {
  test('presets fit the server cap', () => {
    expect(EVAL_PRESETS.length).toBeLessThanOrEqual(MAX_EVAL_CONFIGS);
    expect(EVAL_PRESETS[0].config).toEqual({ searchType: 'fts', algorithm: 'bm25', name: 'fts/bm25' });
  });

  test('parseKeys trims and drops blanks', () => {
    expect(parseKeys(' a.md, ,b.md ')).toEqual(['a.md', 'b.md']);
    expect(parseKeys('')).toEqual([]);
  });

  test('parseGrades reads key=0..3 pairs', () => {
    expect(parseGrades('a.md=3, b=c.md = 0')).toEqual({ 'a.md': 3, 'b=c.md': 0 });
    expect(parseGrades('')).toEqual({});
  });

  test.each(['a.md', '=2', 'a.md=4', 'a.md=x', 'a.md=1.5', ' =1'])('parseGrades refuses %s', (text) => {
    expect(() => parseGrades(text)).toThrow(/must be key=0..3/);
  });

  test('formatGrades', () => {
    expect(formatGrades({ 'a.md': 3, b: 1 })).toBe('a.md=3, b=1');
    expect(formatGrades(undefined)).toBe('');
  });

  test.each([[25, 25], ['7', 7], [500, 100], [0, 10], ['x', 10], [3.9, 3]])('clampK(%s) = %s', (input, want) => {
    expect(clampK(input)).toBe(want);
  });

  test('bestConfig skips failures', () => {
    expect(bestConfig([
      { config: 'fts', ndcgAtK: 0.5 },
      { config: 'vector', error: 'no provider' },
      { config: 'hybrid', ndcgAtK: 0.7 },
      { config: 'other', ndcgAtK: 0.6 },
    ])).toBe('hybrid');
    expect(bestConfig([{ config: 'v', error: 'x' }])).toBeNull();
    expect(bestConfig(undefined)).toBeNull();
  });

  test('formatScore', () => {
    expect(formatScore(0.82547)).toBe('0.825');
    expect(formatScore(undefined)).toBe('—');
  });
});
