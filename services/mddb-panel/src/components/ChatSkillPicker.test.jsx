// @vitest-environment jsdom
// ChatSkillPicker (#404): the ordered skills of a scenario, and ChatTabs, the
// Chat page's tab list.
import { describe, test, expect, vi, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent } from '@testing-library/react';
import { useState } from 'react';
import ChatSkillPicker from './ChatSkillPicker';
import ChatTabs from './ChatTabs';

afterEach(cleanup);

const stored = [{ name: 'tone', description: 'Friendly' }, { name: 'triage' }, { name: 'refunds' }];

/** The picker with its own state, reporting each change to `spy`. */
function Harness({ initial, available, spy }) {
  const [selected, setSelected] = useState(initial);
  return <ChatSkillPicker selected={selected} available={available} onChange={(v) => { spy(v); setSelected(v); }} />;
}

describe('ChatSkillPicker', () => {
  test('adds, moves and removes skills in order; marks a missing name', () => {
    const spy = vi.fn();
    render(<Harness initial={['ghost']} available={stored} spy={spy} />);
    expect(screen.getByText('missing')).toBeTruthy();
    const add = screen.getByLabelText('Add a skill');
    expect([...add.options].map((o) => o.textContent)).toEqual(['tone — Friendly', 'triage', 'refunds']);
    fireEvent.change(add, { target: { value: 'refunds' } });
    fireEvent.click(screen.getByRole('button', { name: 'Add' }));
    expect(spy).toHaveBeenLastCalledWith(['ghost', 'refunds']);
    fireEvent.click(screen.getByRole('button', { name: 'Add' })); // the first left: tone
    expect(spy).toHaveBeenLastCalledWith(['ghost', 'refunds', 'tone']);
    expect(screen.getByRole('button', { name: 'Move ghost up' }).disabled).toBe(true);
    expect(screen.getByRole('button', { name: 'Move tone down' }).disabled).toBe(true);
    fireEvent.click(screen.getByRole('button', { name: 'Move tone up' }));
    expect(spy).toHaveBeenLastCalledWith(['ghost', 'tone', 'refunds']);
    fireEvent.click(screen.getByRole('button', { name: 'Move ghost down' }));
    expect(spy).toHaveBeenLastCalledWith(['tone', 'ghost', 'refunds']);
    fireEvent.click(screen.getByRole('button', { name: 'Remove ghost' }));
    expect(spy).toHaveBeenLastCalledWith(['tone', 'refunds']);
    fireEvent.click(screen.getByRole('button', { name: 'Add' }));
    expect(spy).toHaveBeenLastCalledWith(['tone', 'refunds', 'triage']);
    expect(screen.getByText('No other skills stored')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Add' }).disabled).toBe(true);
  });

  test('without skills, and without the right to list them', () => {
    const view = render(<ChatSkillPicker selected={[]} available={[]} onChange={vi.fn()} />);
    expect(screen.getByText('No skills attached.')).toBeTruthy();
    view.rerender(<ChatSkillPicker selected={['tone']} available={null} onChange={vi.fn()} />);
    expect(screen.queryByLabelText('Add a skill')).toBeNull();
    expect(screen.queryByText('missing')).toBeNull();
    expect(screen.getByText(/The skill list needs admin/)).toBeTruthy();
  });
});

describe('ChatTabs', () => {
  test('a click and the arrow, Home and End keys move the selection and the focus', () => {
    const onChange = vi.fn();
    const view = render(<ChatTabs tab="scenarios" onChange={onChange} />);
    const [scenarios, skills] = screen.getAllByRole('tab');
    expect(scenarios.getAttribute('aria-selected')).toBe('true');
    expect(scenarios.getAttribute('aria-controls')).toBe('chat-panel-scenarios');
    expect(skills.tabIndex).toBe(-1);
    fireEvent.click(skills);
    expect(onChange).toHaveBeenLastCalledWith('skills');
    fireEvent.keyDown(scenarios, { key: 'ArrowRight' });
    expect(onChange).toHaveBeenLastCalledWith('skills');
    expect(document.activeElement).toBe(skills);
    fireEvent.keyDown(scenarios, { key: 'ArrowLeft' }); // wraps round
    expect(onChange).toHaveBeenLastCalledWith('skills');
    fireEvent.keyDown(skills, { key: 'Home' });
    expect(onChange).toHaveBeenLastCalledWith('scenarios');
    fireEvent.keyDown(skills, { key: 'End' });
    expect(onChange).toHaveBeenLastCalledWith('skills');
    onChange.mockClear();
    fireEvent.keyDown(skills, { key: 'Enter' });
    expect(onChange).not.toHaveBeenCalled();
    view.rerender(<ChatTabs tab="skills" onChange={onChange} />);
    expect(screen.getAllByRole('tab')[1].tabIndex).toBe(0);
  });
});
