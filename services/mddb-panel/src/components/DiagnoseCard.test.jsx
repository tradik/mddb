// @vitest-environment jsdom
// DiagnoseCard: the opt-in parts of the diagnose bundle (OPS-025) are sent
// as chosen, the file is saved, and a refusal (403 for a non-admin) is shown.
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, waitFor } from '@testing-library/react';

vi.mock('../lib/mddb-client', () => ({ default: { downloadDiagnose: vi.fn() } }));
vi.mock('../lib/download', () => ({ saveBlob: vi.fn() }));

const { default: mddbClient } = await import('../lib/mddb-client');
const { saveBlob } = await import('../lib/download');
const { default: DiagnoseCard } = await import('./DiagnoseCard');

beforeEach(() => {
  vi.mocked(mddbClient.downloadDiagnose).mockReset();
  vi.mocked(saveBlob).mockReset();
});
afterEach(cleanup);

describe('DiagnoseCard', () => {
  test('downloads the default bundle and saves it', async () => {
    const file = { blob: new Blob(['x']), filename: 'mddb-diagnose-1.tar.gz' };
    vi.mocked(mddbClient.downloadDiagnose).mockResolvedValue(file);
    render(<DiagnoseCard />);
    fireEvent.click(screen.getByRole('button', { name: 'Download diagnose bundle' }));
    await waitFor(() => expect(saveBlob).toHaveBeenCalledWith(file, 'mddb-diagnose.tar.gz'));
    expect(mddbClient.downloadDiagnose).toHaveBeenCalledWith({ profiles: false, integrity: false, keepNames: false });
    expect(screen.getByRole('button', { name: 'Download diagnose bundle' }).disabled).toBe(false);
  });

  test('sends the ticked options and shows the busy state', async () => {
    let resolve;
    vi.mocked(mddbClient.downloadDiagnose).mockReturnValue(new Promise((r) => { resolve = r; }));
    render(<DiagnoseCard />);
    fireEvent.click(screen.getByLabelText('Goroutine and heap profiles'));
    fireEvent.click(screen.getByLabelText(/Keep collection names/));
    fireEvent.click(screen.getByRole('button', { name: 'Download diagnose bundle' }));
    expect(screen.getByRole('button', { name: 'Collecting…' }).disabled).toBe(true);
    expect(mddbClient.downloadDiagnose).toHaveBeenCalledWith({ profiles: true, integrity: false, keepNames: true });
    resolve({ blob: new Blob([]), filename: null });
    await screen.findByRole('button', { name: 'Download diagnose bundle' });
  });

  test('shows a refusal', async () => {
    vi.mocked(mddbClient.downloadDiagnose).mockRejectedValue(new Error('API Error (403): admin access required'));
    render(<DiagnoseCard />);
    fireEvent.click(screen.getByRole('button', { name: 'Download diagnose bundle' }));
    expect((await screen.findByRole('alert')).textContent).toBe('API Error (403): admin access required');
    expect(saveBlob).not.toHaveBeenCalled();
  });
});
