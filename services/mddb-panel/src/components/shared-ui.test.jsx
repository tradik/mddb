// @vitest-environment jsdom
// The shared pieces of the 2.19.0 pages: the confirm dialog (focus, Escape,
// Tab trap), the JSON textarea, the API error notice (403 → "Not
// permitted"), the collection picker and the one-time secret.
import { describe, test, expect, vi, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, act } from '@testing-library/react';
import ConfirmDialog from './ConfirmDialog';
import JsonTextarea from './JsonTextarea';
import ApiErrorNotice from './ApiErrorNotice';
import CollectionPicker from './CollectionPicker';
import OneTimeSecret from './OneTimeSecret';
import { useStore } from '../lib/store';

afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

describe('ConfirmDialog', () => {
  test('focuses Cancel, names what is affected, confirms and gives focus back on close', () => {
    const onConfirm = vi.fn();
    const onCancel = vi.fn();
    const Host = ({ open }) => (
      <>
        <button type="button">opener</button>
        {open && <ConfirmDialog title="Delete schema" message={<>Delete the schema of <strong>blog</strong>?</>} onConfirm={onConfirm} onCancel={onCancel} />}
      </>
    );
    const { rerender } = render(<Host open={false} />);
    screen.getByText('opener').focus();
    rerender(<Host open />);
    const dialog = screen.getByRole('alertdialog', { name: 'Delete schema' });
    expect(dialog.getAttribute('aria-modal')).toBe('true');
    expect(screen.getByText('blog')).toBeTruthy();
    expect(document.activeElement).toBe(screen.getByRole('button', { name: 'Cancel' }));
    fireEvent.click(screen.getByRole('button', { name: 'Delete' }));
    expect(onConfirm).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(onCancel).toHaveBeenCalledTimes(1);
    rerender(<Host open={false} />);
    expect(document.activeElement).toBe(screen.getByText('opener'));
  });

  test('Escape cancels; Tab and Shift+Tab stay inside; other keys do nothing', () => {
    const onCancel = vi.fn();
    render(<ConfirmDialog title="Restore" message="Replace the database?" confirmLabel="Restore" onConfirm={() => {}} onCancel={onCancel} />);
    const dialog = screen.getByRole('alertdialog');
    const cancel = screen.getByRole('button', { name: 'Cancel' });
    const confirm = screen.getByRole('button', { name: 'Restore' });
    fireEvent.keyDown(dialog, { key: 'Tab', shiftKey: true });
    expect(document.activeElement).toBe(confirm);
    fireEvent.keyDown(dialog, { key: 'Tab' });
    expect(document.activeElement).toBe(cancel);
    // In the middle of the cycle the browser moves focus itself.
    fireEvent.keyDown(dialog, { key: 'Tab', shiftKey: false });
    cancel.focus();
    fireEvent.keyDown(dialog, { key: 'Tab', shiftKey: true });
    expect(document.activeElement).toBe(confirm);
    confirm.focus();
    fireEvent.keyDown(dialog, { key: 'Tab', shiftKey: true });
    expect(document.activeElement).toBe(confirm);
    cancel.focus();
    fireEvent.keyDown(dialog, { key: 'Tab' });
    expect(document.activeElement).toBe(cancel);
    fireEvent.keyDown(dialog, { key: 'a' });
    expect(onCancel).not.toHaveBeenCalled();
    fireEvent.keyDown(dialog, { key: 'Escape' });
    expect(onCancel).toHaveBeenCalledTimes(1);
  });

  test('while busy the confirm button is disabled and says so', () => {
    render(<ConfirmDialog title="t" message="m" busy onConfirm={() => {}} onCancel={() => {}} />);
    expect(screen.getByRole('button', { name: 'Working…' }).disabled).toBe(true);
  });
});

describe('JsonTextarea', () => {
  test('is labelled, reports changes and ties the hint and the error to the field', () => {
    const onChange = vi.fn();
    const { rerender } = render(<JsonTextarea label="Schema" value="" onChange={onChange} hint="A JSON object." />);
    const field = screen.getByLabelText('Schema');
    expect(field.getAttribute('aria-invalid')).toBeNull();
    expect(screen.getByText('A JSON object.').id).toBe(field.getAttribute('aria-describedby'));
    fireEvent.change(field, { target: { value: '{' } });
    expect(onChange).toHaveBeenCalledWith('{');

    rerender(<JsonTextarea label="Schema" value="{" onChange={onChange} error="Unexpected end" />);
    expect(field.getAttribute('aria-invalid')).toBe('true');
    expect(field.getAttribute('aria-describedby')).toBe(screen.getByText('Unexpected end').id);

    rerender(<JsonTextarea label="Schema" value="{}" onChange={onChange} />);
    expect(field.getAttribute('aria-describedby')).toBeNull();
  });
});

describe('ApiErrorNotice', () => {
  test('nothing without an error; a plain message or a string as written', () => {
    const { container, rerender } = render(<ApiErrorNotice error={null} />);
    expect(container.textContent).toBe('');
    rerender(<ApiErrorNotice error={new Error('API Error (400): invalid schema')} />);
    expect(screen.getByRole('alert').textContent).toBe('API Error (400): invalid schema');
    rerender(<ApiErrorNotice error="Pick a collection." />);
    expect(screen.getByRole('alert').textContent).toBe('Pick a collection.');
  });

  test('a 403 is "Not permitted" with the server wording and the scope hint', () => {
    const err = Object.assign(new Error('API Error (403): forbidden — long hint'), { status: 403, body: { error: 'forbidden' } });
    const { rerender } = render(<ApiErrorNotice error={err} collection="blog" admin />);
    expect(screen.getByText('Not permitted')).toBeTruthy();
    expect(screen.getByText('forbidden')).toBeTruthy();
    expect(screen.getByText(/covers that collection only/)).toBeTruthy();
    const bare = Object.assign(new Error('API Error (403): nope'), { status: 403 });
    rerender(<ApiErrorNotice error={bare} />);
    expect(screen.getByText('API Error (403): nope')).toBeTruthy();
  });
});

describe('CollectionPicker', () => {
  test('lists the collections from the stats, with an optional empty choice', () => {
    useStore.setState({ stats: { collections: [{ name: 'a' }, { name: 'b' }] } });
    const onChange = vi.fn();
    const { rerender } = render(<CollectionPicker value="a" onChange={onChange} />);
    const select = screen.getByLabelText('Collection');
    expect([...select.options].map((o) => o.value)).toEqual(['a', 'b']);
    fireEvent.change(select, { target: { value: 'b' } });
    expect(onChange).toHaveBeenCalledWith('b');
    rerender(<CollectionPicker value="" onChange={onChange} label="Target" placeholder="All collections" />);
    expect([...screen.getByLabelText('Target').options].map((o) => o.textContent)).toEqual(['All collections', 'a', 'b']);
    useStore.setState({ stats: null });
    rerender(<CollectionPicker value="" onChange={onChange} />);
    expect(screen.getByLabelText('Collection').options).toHaveLength(0);
  });
});

describe('OneTimeSecret', () => {
  test('copies the key, announces it, and dismisses', async () => {
    vi.useFakeTimers();
    const writeText = vi.fn().mockResolvedValue();
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true });
    const onDismiss = vi.fn();
    render(<OneTimeSecret secret="mddb_live_x" onDismiss={onDismiss} />);
    expect(screen.getByText('mddb_live_x')).toBeTruthy();
    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Copy key' })));
    expect(writeText).toHaveBeenCalledWith('mddb_live_x');
    expect(screen.getByRole('status').textContent).toBe('Copied to the clipboard.');
    act(() => vi.advanceTimersByTime(2000));
    expect(screen.getByRole('status').textContent).toBe('');
    fireEvent.click(screen.getByRole('button', { name: 'Dismiss' }));
    expect(onDismiss).toHaveBeenCalledTimes(1);
  });
});
