// @vitest-environment jsdom
// TrainingPanel: what the module is, building a dataset, starting a run or
// a trial run with the profile's parameters, and the runs with their log,
// cancel, download, evaluation and delete — and every refusal shown.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, waitFor, act } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({
  default: {
    getTrainingProfiles: vi.fn(), listTrainingDatasets: vi.fn(), listTrainingRuns: vi.fn(), buildTrainingDataset: vi.fn(),
    deleteTrainingDataset: vi.fn(), downloadTrainingDataset: vi.fn(), startTrainingRun: vi.fn(), getTrainingRun: vi.fn(),
    cancelTrainingRun: vi.fn(), evaluateTrainingRun: vi.fn(), deleteTrainingRun: vi.fn(), downloadTrainingResult: vi.fn(),
  },
}));
vi.mock('../lib/download', () => ({ saveBlob: vi.fn() }));

const { default: client } = await import('../lib/mddb-client');
const { saveBlob } = await import('../lib/download');
const { useStore } = await import('../lib/store');
const { default: TrainingPanel } = await import('./TrainingPanel');
const { TRAINING_POLL_MS } = await import('../lib/training');

const profile = {
  name: 'small', description: 'A small trainer', trialRecords: 100, running: 0,
  parameters: [{ name: 'epochs', type: 'int', min: 1, max: 10, default: 3 }, { name: 'fast', type: 'bool' }],
  evaluate: { baseline: true, parameters: null },
};
const dataset = { id: 'ds_1', name: 'recipes set', status: 'completed', summary: {
  format: 'pairs', records: 2, trainRecords: 1, validationRecords: 1, bytes: 10,
  collections: [{ name: 'recipes', license: 'CC0', documents: 2, records: 2, duplicates: 0, skipped: 0 }], preview: [],
} };
const run = { id: 'run_1', profile: 'small', status: 'completed', result: { file: 'model.gguf', bytes: 5 }, parameters: { epochs: '3' } };

beforeEach(() => {
  for (const fn of Object.values(client)) vi.mocked(fn).mockReset();
  vi.mocked(saveBlob).mockReset();
  useStore.setState({ stats: { collections: [{ name: 'recipes' }, { name: 'techniques' }] } });
  vi.mocked(client.getTrainingProfiles).mockResolvedValue({ configured: true, profiles: [profile], errors: ['trainer 2 ("x"): command: not found'] });
  vi.mocked(client.listTrainingDatasets).mockResolvedValue({ jobs: [dataset] });
  vi.mocked(client.listTrainingRuns).mockResolvedValue({ jobs: [run] });
  vi.mocked(client.getTrainingRun).mockResolvedValue({ job: run, log: '=== training ===' });
  vi.spyOn(window, 'confirm').mockReturnValue(true);
});
afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

describe('TrainingPanel', () => {
  test('says what it is, which trainers there are, and links the guide', async () => {
    render(<TrainingPanel />);
    expect(await screen.findByText(/Trainers configured: small\./)).toBeTruthy();
    expect(screen.getByText(/does not train models itself/)).toBeTruthy();
    expect(screen.getByText(/Profile refused: trainer 2/)).toBeTruthy();
    expect(screen.getByRole('link', { name: /step by step/ }).getAttribute('href')).toMatch(/MODEL-TRAINING-GUIDE\.md$/);
    expect(screen.getByText('A small trainer')).toBeTruthy();
  });

  test('without a trainer only datasets are offered', async () => {
    vi.mocked(client.getTrainingProfiles).mockResolvedValue({ configured: false, profiles: [], errors: [] });
    vi.mocked(client.listTrainingDatasets).mockResolvedValue({ jobs: [] });
    vi.mocked(client.listTrainingRuns).mockResolvedValue({ jobs: [] });
    render(<TrainingPanel />);
    expect(await screen.findByText(/No trainer is configured yet/)).toBeTruthy();
    expect(screen.getByText(/No trainer is configured on this server/)).toBeTruthy();
    expect(screen.getByText('No runs yet.')).toBeTruthy();
  });

  test('before the stats load there is no collection to choose', async () => {
    useStore.setState({ stats: null });
    render(<TrainingPanel />);
    const list = await screen.findByLabelText('Collections');
    expect(list.querySelectorAll('option')).toHaveLength(0);
    expect(screen.getByRole('button', { name: 'Build dataset' }).disabled).toBe(true);
  });

  test('a failed load is shown, and Reload tries again', async () => {
    vi.mocked(client.getTrainingProfiles).mockRejectedValueOnce(new Error('API Error (404): feature disabled'));
    render(<TrainingPanel />);
    expect((await screen.findByRole('alert')).textContent).toMatch(/feature disabled/);
    fireEvent.click(screen.getByRole('button', { name: 'Reload' }));
    await waitFor(() => expect(screen.queryByRole('alert')).toBeNull());
  });

  test('builds a pairs dataset from the chosen collections', async () => {
    vi.mocked(client.buildTrainingDataset).mockResolvedValue({ jobId: 'ds_2', job: { id: 'ds_2' } });
    render(<TrainingPanel />);
    await screen.findByText(/Trainers configured/);
    const build = screen.getByRole('button', { name: 'Build dataset' });
    expect(build.disabled).toBe(true);
    const select = screen.getByLabelText('Collections');
    for (const o of select.options) o.selected = true;
    fireEvent.change(select);
    fireEvent.change(screen.getByLabelText('Input'), { target: { value: 'A {meta.cuisine} dish' } });
    fireEvent.change(screen.getByLabelText('System text (optional)'), { target: { value: 'S' } });
    fireEvent.change(screen.getByLabelText('Metadata key'), { target: { value: 'cuisine' } });
    fireEvent.change(screen.getByLabelText('Value'), { target: { value: 'french' } });
    fireEvent.click(screen.getByLabelText(/record no licence/));
    fireEvent.click(screen.getByLabelText('Shuffle'));
    expect(screen.getByLabelText('Shuffle seed').disabled).toBe(true);
    fireEvent.click(build);
    await waitFor(() => expect(client.buildTrainingDataset).toHaveBeenCalled());
    expect(vi.mocked(client.buildTrainingDataset).mock.calls[0][0]).toMatchObject({
      collections: ['recipes', 'techniques'], format: 'pairs', shuffle: false, acknowledgeUnlicensed: true, validationRatio: 0.1,
      filterMeta: { cuisine: ['french'] }, pairs: { input: 'A {meta.cuisine} dish', output: '{content}', system: 'S' },
    });
    expect(client.listTrainingDatasets).toHaveBeenCalledTimes(2);
  });

  test('switching to text hides the fields; a refusal is shown', async () => {
    vi.mocked(client.buildTrainingDataset).mockRejectedValue(new Error('API Error (400): notes record no licence'));
    render(<TrainingPanel />);
    await screen.findByText(/Trainers configured/);
    fireEvent.change(screen.getByLabelText('Format'), { target: { value: 'text' } });
    expect(screen.queryByLabelText('Input')).toBeNull();
    const select = screen.getByLabelText('Collections');
    select.options[0].selected = true;
    fireEvent.change(select);
    fireEvent.change(screen.getByLabelText('Name (optional)', { selector: '#training-name' }), { target: { value: 'set' } });
    fireEvent.click(screen.getByRole('button', { name: 'Build dataset' }));
    expect((await screen.findByRole('alert')).textContent).toMatch(/record no licence/);
  });

  test('starts a run and a trial run with the parameters', async () => {
    vi.mocked(client.startTrainingRun).mockResolvedValue({ jobId: 'run_2', job: { id: 'run_2' } });
    render(<TrainingPanel />);
    await screen.findByText(/Trainers configured/);
    const start = screen.getByRole('button', { name: 'Start' });
    expect(start.disabled).toBe(true);
    fireEvent.change(screen.getByLabelText('Dataset'), { target: { value: 'ds_1' } });
    fireEvent.change(screen.getByLabelText('epochs'), { target: { value: '5' } });
    fireEvent.click(screen.getByRole('switch', { name: 'fast' }));
    fireEvent.change(screen.getByLabelText('Name (optional)', { selector: '#training-run-name' }), { target: { value: 'first' } });
    fireEvent.click(screen.getByLabelText(/before and after/));
    fireEvent.click(screen.getByRole('button', { name: 'Trial run' }));
    await waitFor(() => expect(client.startTrainingRun).toHaveBeenCalledTimes(1));
    expect(vi.mocked(client.startTrainingRun).mock.calls[0][0]).toEqual({
      profile: 'small', datasetId: 'ds_1', parameters: { epochs: 5, fast: true }, trial: true, name: 'first', evaluate: false,
    });
    expect(screen.getByText(/small sample \(100 records\)/)).toBeTruthy();
    fireEvent.click(start);
    await waitFor(() => expect(client.startTrainingRun).toHaveBeenCalledTimes(2));
    expect(vi.mocked(client.startTrainingRun).mock.calls[1][0].trial).toBe(false);
    await waitFor(() => expect(client.getTrainingRun).toHaveBeenCalledWith('run_2'));
  });

  test('a refused run is shown; another profile resets the values', async () => {
    const other = { name: 'other', trialRecords: 5, parameters: null };
    vi.mocked(client.getTrainingProfiles).mockResolvedValue({ configured: true, profiles: [profile, other], errors: [] });
    vi.mocked(client.startTrainingRun).mockRejectedValue(new Error('API Error (409): the profile already runs as many jobs as it allows'));
    render(<TrainingPanel />);
    await screen.findByText(/Trainers configured: small, other/);
    fireEvent.change(screen.getByLabelText('Trainer'), { target: { value: 'other' } });
    expect(screen.queryByLabelText('epochs')).toBeNull();
    expect(screen.queryByLabelText(/before and after/)).toBeNull();
    fireEvent.change(screen.getByLabelText('Dataset'), { target: { value: 'ds_1' } });
    fireEvent.click(screen.getByRole('button', { name: 'Start' }));
    expect((await screen.findByRole('alert')).textContent).toMatch(/as many jobs/);
    expect(vi.mocked(client.startTrainingRun).mock.calls[0][0]).toEqual({ profile: 'other', datasetId: 'ds_1', parameters: {}, trial: false });
  });

  test('a run opens with its log; download, evaluate, delete; datasets download and delete', async () => {
    vi.mocked(client.downloadTrainingResult).mockResolvedValue({ blob: 'b', filename: 'model.gguf' });
    vi.mocked(client.downloadTrainingDataset).mockResolvedValue({ blob: 'd', filename: null });
    vi.mocked(client.evaluateTrainingRun).mockRejectedValue(new Error('API Error (409): busy'));
    render(<TrainingPanel />);
    fireEvent.click(await screen.findByRole('button', { name: /run_1/ }));
    expect(await screen.findByText('=== training ===')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'model.gguf (5 B)' }));
    await waitFor(() => expect(saveBlob).toHaveBeenCalledWith({ blob: 'b', filename: 'model.gguf' }, 'model.gguf'));
    fireEvent.click(screen.getByRole('button', { name: 'Evaluate again' }));
    expect((await screen.findByRole('alert')).textContent).toMatch(/busy/);
    fireEvent.click(screen.getByRole('button', { name: 'Training part' }));
    await waitFor(() => expect(saveBlob).toHaveBeenCalledWith({ blob: 'd', filename: null }, 'ds_1-train.jsonl'));
    fireEvent.click(screen.getByRole('button', { name: 'Delete dataset recipes set' }));
    await waitFor(() => expect(client.deleteTrainingDataset).toHaveBeenCalledWith('ds_1'));
    fireEvent.click(screen.getByRole('button', { name: 'Delete run run_1' }));
    await waitFor(() => expect(client.deleteTrainingRun).toHaveBeenCalledWith('run_1'));
    await waitFor(() => expect(screen.queryByText('=== training ===')).toBeNull());
  });

  test('a delete can be called off', async () => {
    window.confirm.mockReturnValue(false);
    render(<TrainingPanel />);
    fireEvent.click(await screen.findByRole('button', { name: /run_1/ }));
    await screen.findByText('=== training ===');
    fireEvent.click(screen.getByRole('button', { name: 'Delete run run_1' }));
    fireEvent.click(screen.getByRole('button', { name: 'Delete dataset recipes set' }));
    await waitFor(() => expect(client.listTrainingRuns).toHaveBeenCalledTimes(3));
    expect(client.deleteTrainingRun).not.toHaveBeenCalled();
    expect(client.deleteTrainingDataset).not.toHaveBeenCalled();
  });

  test('polls while a job runs, and cancels it', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const running = { ...run, status: 'processing', stage: 'training', result: undefined, trial: true };
    vi.mocked(client.listTrainingRuns).mockResolvedValue({ jobs: [running] });
    vi.mocked(client.getTrainingRun).mockResolvedValue({ job: running, log: 'working' });
    render(<TrainingPanel />);
    fireEvent.click(await screen.findByRole('button', { name: /run_1 \(trial\)/ }));
    await screen.findByText('working');
    const before = vi.mocked(client.listTrainingRuns).mock.calls.length;
    await act(async () => { await vi.advanceTimersByTimeAsync(TRAINING_POLL_MS + 10); });
    expect(vi.mocked(client.listTrainingRuns).mock.calls.length).toBeGreaterThan(before);
    expect(vi.mocked(client.getTrainingRun).mock.calls.length).toBeGreaterThan(1);
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    await waitFor(() => expect(client.cancelTrainingRun).toHaveBeenCalledWith('run_1'));
  });

  test('polls the lists alone while only a dataset is building', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    vi.mocked(client.listTrainingDatasets).mockResolvedValue({ jobs: [{ id: 'ds_3', status: 'pending' }] });
    render(<TrainingPanel />);
    await screen.findByText('Waiting', { exact: false });
    await act(async () => { await vi.advanceTimersByTimeAsync(TRAINING_POLL_MS + 10); });
    expect(vi.mocked(client.listTrainingDatasets).mock.calls.length).toBeGreaterThan(1);
    expect(client.getTrainingRun).not.toHaveBeenCalled();
  });

  test('a run that cannot be read is reported', async () => {
    vi.mocked(client.getTrainingRun).mockRejectedValue(new Error('API Error (404): training job not found'));
    render(<TrainingPanel />);
    fireEvent.click(await screen.findByRole('button', { name: /run_1/ }));
    expect((await screen.findByRole('alert')).textContent).toMatch(/not found/);
  });
});
