// @vitest-environment jsdom
// The Model training page's display parts: a parameter's field by its type,
// the before/after of an evaluation, a dataset's summary and a run's detail.
import { describe, test, expect, vi, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent } from '@testing-library/react';
import TrainingParamField from './TrainingParamField';
import TrainingEvaluation from './TrainingEvaluation';
import TrainingDatasetSummary from './TrainingDatasetSummary';
import TrainingRunDetail from './TrainingRunDetail';

afterEach(cleanup);

describe('TrainingParamField', () => {
  const field = (param, value) => {
    const onChange = vi.fn();
    render(<TrainingParamField param={param} value={value} onChange={onChange} idPrefix="p" />);
    return onChange;
  };

  test('a number keeps the profile range and sends a number', () => {
    const onChange = field({ name: 'epochs', type: 'int', min: 1, max: 9, description: 'How many passes' }, 3);
    const input = screen.getByLabelText('epochs');
    expect(input.min).toBe('1');
    expect(input.max).toBe('9');
    expect(input.step).toBe('1');
    expect(input.getAttribute('aria-describedby')).toBe('p-epochs-hint');
    fireEvent.change(input, { target: { value: '4' } });
    expect(onChange).toHaveBeenCalledWith('epochs', 4);
  });

  test('a float steps freely, an empty one is left out', () => {
    const onChange = field({ name: 'rate', type: 'float' });
    const input = screen.getByLabelText('rate');
    expect(input.step).toBe('any');
    expect(input.value).toBe('');
    fireEvent.change(input, { target: { value: '0.5' } });
    expect(onChange).toHaveBeenCalledWith('rate', 0.5);
  });

  test('an enum offers its values, and the trainer default unless required', () => {
    const onChange = field({ name: 'method', type: 'enum', values: ['a', 'b'] }, undefined);
    expect(screen.getAllByRole('option').map((o) => o.textContent)).toEqual(["(the trainer's own default)", 'a', 'b']);
    fireEvent.change(screen.getByLabelText('method'), { target: { value: 'b' } });
    expect(onChange).toHaveBeenCalledWith('method', 'b');
    cleanup();
    field({ name: 'm', type: 'enum', values: ['x'], required: true }, 'x');
    expect(screen.getByText('m (required)')).toBeTruthy();
    expect(screen.getAllByRole('option')).toHaveLength(1);
  });

  test('a bool is a switch', () => {
    const onChange = field({ name: 'fast', type: 'bool', description: 'd' }, false);
    fireEvent.click(screen.getByRole('switch', { name: 'fast' }));
    expect(onChange).toHaveBeenCalledWith('fast', true);
  });

  test('a string keeps its length limit', () => {
    const onChange = field({ name: 'tag', type: 'string', maxLength: 16 }, 'v1');
    const input = screen.getByLabelText('tag');
    expect(input.maxLength).toBe(16);
    fireEvent.change(input, { target: { value: 'v2' } });
    expect(onChange).toHaveBeenCalledWith('tag', 'v2');
  });
});

describe('TrainingEvaluation', () => {
  test('nothing, or the reason it was skipped', () => {
    const { container } = render(<TrainingEvaluation evaluation={null} />);
    expect(container.textContent).toBe('');
    render(<TrainingEvaluation evaluation={{ skipped: 'no validation part' }} />);
    expect(screen.getByText('Not evaluated: no validation part.')).toBeTruthy();
  });

  test('scores before and after, the difference, metrics and samples as text', () => {
    render(<TrainingEvaluation evaluation={{
      base: { report: { score: 0.4, metrics: { exact: 0.4 } } },
      result: { report: { score: 0.7, metrics: { exact: 0.7, f1: 0.5 }, samples: [{ input: '<b>i</b>', expected: 'e', actual: 'a', correct: false }, { input: 'j', expected: 'e', actual: 'e', correct: true }] } },
      delta: 0.3,
    }} />);
    expect(screen.getByText('40.0%')).toBeTruthy();
    expect(screen.getByText('70.0%')).toBeTruthy();
    expect(screen.getByText('+30.0 pts')).toBeTruthy();
    expect(screen.getByText('–')).toBeTruthy(); // f1 has no baseline
    expect(screen.getByText('<b>i</b>')).toBeTruthy();
    expect(screen.getByText('no')).toBeTruthy();
    expect(screen.getByText('yes')).toBeTruthy();
  });

  test('a worse score warns, a missing baseline says so', () => {
    render(<TrainingEvaluation evaluation={{
      result: { report: { score: 0.2 } }, base: { error: 'exit 2' }, delta: -0.1, notes: ['The tuned model did not score higher.'],
    }} />);
    expect(screen.getByText('no baseline')).toBeTruthy();
    expect(screen.getByText('-10.0 pts')).toBeTruthy();
    expect(screen.getByText(/did not score higher/)).toBeTruthy();
    expect(screen.getByText(/exit 2/)).toBeTruthy();
    cleanup();
    render(<TrainingEvaluation evaluation={{ result: { error: 'bad' } }} />);
    expect(screen.getAllByText('–')).toHaveLength(2);
  });
});

describe('TrainingDatasetSummary', () => {
  const built = {
    id: 'ds_1', status: 'completed',
    summary: {
      format: 'pairs', records: 3, trainRecords: 2, validationRecords: 1, bytes: 2048, unlicensedAcknowledged: ['notes'],
      collections: [{ name: 'recipes', license: 'CC-BY-4.0', documents: 4, records: 2, duplicates: 1, skipped: 1 }, { name: 'notes', documents: 1, records: 1, duplicates: 0, skipped: 0 }],
      preview: [{ input: 'q', output: 'a' }],
    },
  };

  test('the summary, licences, preview and the two downloads', () => {
    const onDownload = vi.fn();
    const onDelete = vi.fn();
    render(<TrainingDatasetSummary dataset={built} onDownload={onDownload} onDelete={onDelete} />);
    expect(screen.getByText(/3 records \(pairs\): 2 to train on, 1 held back · 2.0 KB/)).toBeTruthy();
    expect(screen.getByText(/acknowledged: notes/)).toBeTruthy();
    expect(screen.getByText('none recorded')).toBeTruthy();
    expect(screen.getByText('{"input":"q","output":"a"}')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Validation part' }));
    expect(onDownload).toHaveBeenCalledWith('ds_1', 'validation');
    fireEvent.click(screen.getByRole('button', { name: 'Delete dataset ds_1' }));
    expect(onDelete).toHaveBeenCalledWith('ds_1');
  });

  test('a failed dataset shows its error and no downloads', () => {
    render(<TrainingDatasetSummary dataset={{ id: 'ds_2', name: 'mine', status: 'failed', error: 'no records' }} onDownload={vi.fn()} onDelete={vi.fn()} />);
    expect(screen.getByText('no records')).toBeTruthy();
    expect(screen.getByText('mine')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Training part' })).toBeNull();
  });
});

describe('TrainingRunDetail', () => {
  const handlers = () => ({ onCancel: vi.fn(), onDownload: vi.fn(), onEvaluate: vi.fn(), onDelete: vi.fn() });

  test('a running trial can be cancelled, not deleted', () => {
    const h = handlers();
    render(<TrainingRunDetail run={{ id: 'run_1', profile: 'p', status: 'processing', stage: 'training', trial: true, progress: 50, progressMessage: 'half' }} log="" {...h} />);
    expect(screen.getByText('trial')).toBeTruthy();
    expect(screen.getByText(/p · Running · stage training · 50% half/)).toBeTruthy();
    expect(screen.getByText('Nothing written yet.')).toBeTruthy();
    expect(screen.queryByRole('button', { name: /Delete run/ })).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(h.onCancel).toHaveBeenCalledWith('run_1');
  });

  test('a finished run offers its result, another evaluation and deletion', () => {
    const h = handlers();
    render(<TrainingRunDetail log="=== training ===" {...h} run={{
      id: 'run_2', name: 'full', profile: 'p', status: 'completedWithWarnings', exitCode: 0, warnings: ['an evaluation stage failed'],
      result: { file: 'model.gguf', bytes: 10 }, parameters: { epochs: '3' }, evaluation: { skipped: 'text dataset' },
    }} />);
    expect(screen.getByText(/exit code 0/)).toBeTruthy();
    expect(screen.getByText('an evaluation stage failed')).toBeTruthy();
    expect(screen.getByText('Parameters: epochs=3')).toBeTruthy();
    expect(screen.getByText('=== training ===')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'model.gguf (10 B)' }));
    fireEvent.click(screen.getByRole('button', { name: 'Evaluate again' }));
    fireEvent.click(screen.getByRole('button', { name: 'Delete run full' }));
    expect(h.onDownload).toHaveBeenCalledWith('run_2');
    expect(h.onEvaluate).toHaveBeenCalledWith('run_2');
    expect(h.onDelete).toHaveBeenCalledWith('run_2');
  });

  test('a failed run shows why', () => {
    render(<TrainingRunDetail log="boom" {...handlers()} run={{ id: 'run_3', profile: 'p', status: 'failed', error: 'training: the command exited with code 3' }} />);
    expect(screen.getByText(/exited with code 3/)).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Evaluate again' })).toBeNull();
  });
});
