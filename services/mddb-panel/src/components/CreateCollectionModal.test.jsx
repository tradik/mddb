// @vitest-environment jsdom
// CreateCollectionModal: the name validation (required, allowed characters),
// the trimmed name handed to onCreate, and the two ways to close the modal.
import { describe, test, expect, vi, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent } from '@testing-library/react';

const { default: CreateCollectionModal } = await import('./CreateCollectionModal');

afterEach(cleanup);

function setup() {
  const onClose = vi.fn();
  const onCreate = vi.fn();
  render(<CreateCollectionModal onClose={onClose} onCreate={onCreate} />);
  const input = screen.getByPlaceholderText('e.g., blog, docs, notes');
  const submit = screen.getByRole('button', { name: 'Create Collection' });
  return { onClose, onCreate, input, submit };
}

describe('CreateCollectionModal', () => {
  test('an empty or blank name is refused', () => {
    const { onCreate, input, submit } = setup();
    fireEvent.click(submit);
    expect(screen.getByText('Collection name is required')).toBeTruthy();
    fireEvent.change(input, { target: { value: '   ' } });
    fireEvent.click(submit);
    expect(screen.getByText('Collection name is required')).toBeTruthy();
    expect(onCreate).not.toHaveBeenCalled();
  });

  test('characters outside letters, digits, _ and - are refused', () => {
    const { onCreate, input, submit } = setup();
    fireEvent.change(input, { target: { value: 'my blog!' } });
    fireEvent.click(submit);
    expect(screen.getByText(/can only contain letters, numbers/)).toBeTruthy();
    expect(onCreate).not.toHaveBeenCalled();
  });

  test('a valid name is trimmed, passed to onCreate and clears the earlier error', () => {
    const { onCreate, input, submit } = setup();
    fireEvent.click(submit);
    expect(screen.getByText('Collection name is required')).toBeTruthy();
    fireEvent.change(input, { target: { value: '  blog_posts-2  ' } });
    fireEvent.click(submit);
    expect(onCreate).toHaveBeenCalledWith('blog_posts-2');
    expect(screen.queryByText('Collection name is required')).toBeNull();
  });

  test('Cancel and the X button both close', () => {
    const { onClose } = setup();
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    fireEvent.click(screen.getAllByRole('button')[0]);
    expect(onClose).toHaveBeenCalledTimes(2);
  });
});
