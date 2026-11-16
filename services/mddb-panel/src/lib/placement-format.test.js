// placement-format (GO-052): move phases, which collections are mid-move,
// the groups a collection can go to, and how far a move got.
import { describe, test, expect } from 'vitest';
import {
  anyMoveRunning, isCollectionMoving, isMoveRunning, lsnProgress, moveTiming, phaseClass, targetGroups,
} from './placement-format';

describe('placement-format', () => {
  test('a move runs until it is done or failed', () => {
    expect(isMoveRunning(undefined)).toBe(false);
    expect(['copying', 'catching-up', 'fenced'].map((phase) => isMoveRunning({ phase }))).toEqual([true, true, true]);
    expect(['done', 'failed'].map((phase) => isMoveRunning({ phase }))).toEqual([false, false]);
  });

  test('polling follows any running move', () => {
    expect(anyMoveRunning(null)).toBe(false);
    expect(anyMoveRunning({ enabled: true })).toBe(false);
    expect(anyMoveRunning({ moves: [{ phase: 'done' }, { phase: 'fenced' }] })).toBe(true);
  });

  test('a collection is moving by the map or by a running move of it', () => {
    expect(isCollectionMoving({ name: 'a', movingTo: 'g2' })).toBe(true);
    expect(isCollectionMoving({ name: 'a' })).toBe(false);
    expect(isCollectionMoving({ name: 'a' }, [{ collection: 'b', phase: 'copying' }])).toBe(false);
    expect(isCollectionMoving({ name: 'a' }, [{ collection: 'a', phase: 'failed' }])).toBe(false);
    expect(isCollectionMoving({ name: 'a' }, [{ collection: 'a', phase: 'copying' }])).toBe(true);
  });

  test('the target groups leave out the current one', () => {
    expect(targetGroups(undefined, 'g1')).toEqual([]);
    expect(targetGroups([{ name: 'g1' }, { name: 'g2' }], 'g1')).toEqual(['g2']);
  });

  test('phase colours, with a neutral one for an unknown phase', () => {
    expect(phaseClass('failed')).toContain('danger');
    expect(phaseClass('done')).toContain('success');
    expect(phaseClass('fenced')).toContain('warning');
    expect(phaseClass('later')).toBe('bg-surface-subtle text-fg-soft');
  });

  test('LSN progress', () => {
    expect(lsnProgress({})).toBe('-');
    expect(lsnProgress({ sourceLsn: 200 })).toBe('0 / 200 (0%)');
    expect(lsnProgress({ sourceLsn: 1000, appliedLsn: 999 })).toBe('999 / 1,000 (99%)');
  });

  test('timing: duration when finished, start time while running', () => {
    expect(moveTiming({ durationMs: 2500 })).toBe('took 2.5s');
    expect(moveTiming({})).toBe('-');
    expect(moveTiming({ startedAt: 'nope' })).toBe('-');
    expect(moveTiming({ startedAt: '2026-11-16T10:00:00Z' })).toMatch(/^started /);
  });
});
