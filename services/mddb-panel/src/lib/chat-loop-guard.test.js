// chat-loop-guard: a scenario's llm.loopGuard as form fields and back (#413).
import { describe, test, expect } from 'vitest';
import { toLoopGuardForm, fromLoopGuardForm, loopGuardProblem, loopGuardModes, loopGuardUnits } from './chat-loop-guard';

const blank = toLoopGuardForm();

describe('toLoopGuardForm', () => {
  test('no guard leaves every field to the chat service', () => {
    expect(blank).toEqual({
      loopGuardMode: 'default', loopGuardNgram: '', loopGuardWindow: '', loopGuardRatio: '',
      loopGuardMinOutput: '', loopGuardUnit: '',
    });
  });

  test('a stored guard fills its fields', () => {
    expect(toLoopGuardForm({ enabled: true, ngram: 16, window: 64, ratio: 0.9, minOutput: 50, unit: 'words' })).toEqual({
      loopGuardMode: 'on', loopGuardNgram: '16', loopGuardWindow: '64', loopGuardRatio: '0.9',
      loopGuardMinOutput: '50', loopGuardUnit: 'words',
    });
    expect(toLoopGuardForm({ enabled: false }).loopGuardMode).toBe('off');
  });
});

describe('fromLoopGuardForm', () => {
  test('nothing set overrides nothing', () => {
    expect(fromLoopGuardForm(blank)).toBeUndefined();
    expect(fromLoopGuardForm({ ...blank, loopGuardNgram: '0', loopGuardRatio: '0' })).toBeUndefined();
  });

  test('every field set survives the round trip', () => {
    const guard = { enabled: true, ngram: 16, window: 64, ratio: 0.9, minOutput: 50, unit: 'words' };
    expect(fromLoopGuardForm(toLoopGuardForm(guard))).toEqual(guard);
    expect(fromLoopGuardForm({ ...blank, loopGuardMode: 'off', loopGuardWindow: 'x' })).toEqual({ enabled: false });
  });
});

describe('loopGuardProblem', () => {
  test.each([
    [{}, null],
    [{ loopGuardRatio: '1' }, null],
    [{ loopGuardRatio: '0' }, 'The loop guard ratio must be above 0 and at most 1.'],
    [{ loopGuardRatio: 'lots' }, 'The loop guard ratio must be above 0 and at most 1.'],
    [{ loopGuardNgram: '64', loopGuardWindow: '32' }, 'The loop guard window may not be shorter than its n-gram.'],
    [{ loopGuardNgram: '64', loopGuardWindow: '64' }, null],
    [{ loopGuardNgram: '64' }, null],
  ])('%j → %j', (fields, problem) => {
    expect(loopGuardProblem({ ...blank, ...fields })).toBe(problem);
  });

  test('every choice has a label', () => {
    [...loopGuardModes, ...loopGuardUnits].forEach((choice) => expect(choice.label).toBeTruthy());
  });
});
