// chat-skill (#404): the skill editor's form state and the ordered skill list.
import { describe, test, expect } from 'vitest';
import { toSkillForm, fromSkillForm, skillProblem, sourceLabel, formatBytes, moveItem } from './chat-skill';

const source = { url: 'https://github.com/acme/skills/blob/main/SKILL.md', commit: '3f2c9ab1e0', fetchedAt: '2026-11-11T10:00:00Z' };

describe('the form', () => {
  test('a new skill is empty; a stored one keeps its source', () => {
    expect(toSkillForm()).toEqual({ name: '', description: '', content: '', source: null });
    expect(toSkillForm({ name: 'tone', description: 'Friendly', content: 'Be kind.', source, version: 2 }))
      .toEqual({ name: 'tone', description: 'Friendly', content: 'Be kind.', source });
  });

  test('the saved body trims the description and sends a source only when there is one', () => {
    expect(fromSkillForm({ name: 'tone', description: ' Friendly ', content: 'Be kind.', source: null }))
      .toEqual({ description: 'Friendly', content: 'Be kind.' });
    expect(fromSkillForm({ name: 'tone', description: '', content: 'x', source }))
      .toEqual({ description: '', content: 'x', source });
  });

  test.each([
    [{ name: ' ', content: 'x' }, 'Give the skill a name.'],
    [{ name: 'a b', content: 'x' }, 'The name may not contain spaces, "/" or "|", or be "version" or "import".'],
    [{ name: 'import', content: 'x' }, 'The name may not contain spaces, "/" or "|", or be "version" or "import".'],
    [{ name: 'version', content: 'x' }, 'The name may not contain spaces, "/" or "|", or be "version" or "import".'],
    [{ name: 'tone', content: '  ' }, 'Write the instructions, or paste a SKILL.md.'],
    [{ name: 'tone', content: 'Be kind.' }, null],
  ])('skillProblem(%o)', (form, want) => {
    expect(skillProblem(form)).toBe(want);
  });
});

describe('labels', () => {
  test('a source names its host, path and short commit', () => {
    expect(sourceLabel(source)).toBe('github.com/acme/skills/blob/main/SKILL.md @ 3f2c9ab');
    expect(sourceLabel({ url: 'https://example.com/s.md' })).toBe('example.com/s.md');
    expect(sourceLabel({ url: 'not a url' })).toBe('not a url');
    expect(sourceLabel(null)).toBe('pasted');
  });

  test('bytes', () => {
    expect(formatBytes()).toBe('0 B');
    expect(formatBytes(1023)).toBe('1023 B');
    expect(formatBytes(32768)).toBe('32.0 KiB');
  });
});

describe('moveItem', () => {
  test('moves up and down, and stays put at an end', () => {
    const list = ['a', 'b', 'c'];
    expect(moveItem(list, 1, -1)).toEqual(['b', 'a', 'c']);
    expect(moveItem(list, 1, 1)).toEqual(['a', 'c', 'b']);
    expect(moveItem(list, 0, -1)).toBe(list);
    expect(moveItem(list, 2, 1)).toBe(list);
    expect(list).toEqual(['a', 'b', 'c']);
  });
});
