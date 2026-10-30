// @vitest-environment jsdom
// SearchPlanningControls: the "Explain plan" box, and the "Split multi-part
// questions" box shown only when a decomposition handler is given.
import { describe, test, expect, vi, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent } from '@testing-library/react';
import SearchPlanningControls from './SearchPlanningControls';

afterEach(cleanup);

describe('SearchPlanningControls', () => {
  test('without a decomposition handler only Explain plan shows', () => {
    const onExplainChange = vi.fn();
    render(<SearchPlanningControls explain={false} onExplainChange={onExplainChange} />);
    expect(screen.queryByLabelText('Split multi-part questions')).toBeNull();
    const box = screen.getByLabelText('Explain plan');
    expect(box.checked).toBe(false);
    fireEvent.click(box);
    expect(onExplainChange).toHaveBeenCalledWith(true);
  });

  test('Explain plan reflects and clears its value', () => {
    const onExplainChange = vi.fn();
    render(<SearchPlanningControls explain onExplainChange={onExplainChange} />);
    const box = screen.getByLabelText('Explain plan');
    expect(box.checked).toBe(true);
    fireEvent.click(box);
    expect(onExplainChange).toHaveBeenCalledWith(false);
  });

  test.each([
    ['off', false, 'auto'],
    ['auto', true, 'off'],
  ])('decomposition %s shows checked=%s and toggles to %s', (value, checked, next) => {
    const onDecompositionChange = vi.fn();
    render(
      <SearchPlanningControls
        explain={false}
        onExplainChange={vi.fn()}
        decomposition={value}
        onDecompositionChange={onDecompositionChange}
      />,
    );
    const box = screen.getByLabelText('Split multi-part questions');
    expect(box.checked).toBe(checked);
    fireEvent.click(box);
    expect(onDecompositionChange).toHaveBeenCalledWith(next);
  });
});
